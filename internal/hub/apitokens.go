package hub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// API-токены хаба — доступ для автоматизации (n8n, CI, скрипты) без
// пароля и сессии браузера. Два способа предъявить токен:
//
//   - Authorization: Bearer nkt_<ключ>_<секрет> — просто, но секрет идёт
//     по сети в каждом запросе; годится в своей сети.
//   - подписанный запрос: X-NKT-API-Key (ключ), X-NKT-API-Timestamp (unix),
//     X-NKT-API-Nonce (одноразовая строка) и X-NKT-API-Signature —
//     hex HMAC-SHA256 секретом от строки
//     «NKT-API-1\n<время>\n<nonce>\n<МЕТОД>\n<путь?запрос>\n<hex sha256 тела>».
//     Секрет по сети не передаётся; подпись живёт минуты и не повторяется.
//     Так токен можно пускать и через промежуточный узел (nkt-edge).
//
// Токену открыт не весь API, а список вызовов автоматизации (tokenPolicy):
// управлять самим хабом (учётки, токены, экспорт, обновление, edge),
// терминал, файлы и веб-сокеты токену недоступны ни с какой ролью.

const (
	tokenPrefix   = "nkt_"
	tokenKeyLen   = 16 // base32 от 10 случайных байт
	tokenSkew     = 5 * time.Minute
	tokenMaxBody  = 1 << 20
	tokenTouchGap = time.Minute
)

var (
	tokenKeyRe   = regexp.MustCompile(`^[a-z2-7]{16}$`)
	tokenNonceRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	tokenB32     = base32.StdEncoding.WithPadding(base32.NoPadding)
)

type tokenCtxKey struct{}

// tokenFromContext — токен, которым пришёл запрос (нет — сессия браузера).
func tokenFromContext(ctx context.Context) (store.APIToken, bool) {
	t, ok := ctx.Value(tokenCtxKey{}).(store.APIToken)
	return t, ok
}

// newTokenSecret — открытая часть ключа и секрет.
func newTokenSecret() (string, string, error) {
	kb := make([]byte, 10)
	sb := make([]byte, 32)
	if _, err := rand.Read(kb); err != nil {
		return "", "", err
	}
	if _, err := rand.Read(sb); err != nil {
		return "", "", err
	}
	return strings.ToLower(tokenB32.EncodeToString(kb)), base64.RawURLEncoding.EncodeToString(sb), nil
}

// bearerToken — строка для заголовка Authorization.
func bearerToken(keyID, secret string) string { return tokenPrefix + keyID + "_" + secret }

// TokenSignature — подпись запроса (её же считает узел n8n и примеры в
// документации).
func TokenSignature(secret, ts, nonce, method, uri string, body []byte) string {
	sum := sha256.Sum256(body)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("NKT-API-1\n" + ts + "\n" + nonce + "\n" + strings.ToUpper(method) + "\n" + uri + "\n" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(m.Sum(nil))
}

// tokenAuth — проверка токенов хаба (auth.TokenAuthenticator).
type tokenAuth struct {
	s     *Server
	fails *auth.AttemptLimiter

	mu        sync.Mutex
	nonces    map[string]time.Time
	lastPrune time.Time
}

func newTokenAuth(s *Server) *tokenAuth {
	return &tokenAuth{s: s, fails: auth.NewAttemptLimiter(), nonces: map[string]time.Time{}}
}

func tokenDeny(code int, key string, args ...any) error {
	return &auth.TokenError{Code: code, Err: msgs.Errorf(key, args...)}
}

// useNonce — одноразовость подписи: та же пара ключ+nonce второй раз не
// пройдёт, пока подпись вообще могла бы пройти по времени.
func (a *tokenAuth) useNonce(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.Sub(a.lastPrune) > time.Minute {
		for k, at := range a.nonces {
			if now.Sub(at) > 2*tokenSkew {
				delete(a.nonces, k)
			}
		}
		a.lastPrune = now
	}
	if _, seen := a.nonces[key]; seen {
		return false
	}
	a.nonces[key] = now
	return true
}

type peerCtxKey struct{}

// rememberPeer — настоящий адрес соединения до middleware.RealIP: тот
// подставляет X-Forwarded-For / X-Real-IP от кого угодно, а список
// разрешённых адресов токена так обойти нельзя.
func rememberPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerCtxKey{}, r.RemoteAddr)))
	})
}

// tokenClientIP — адрес клиента токена. Заголовкам прокси верим, только
// если соединение пришло с loopback (свой обратный прокси или туннель
// nkt-edge), иначе — адрес соединения.
func tokenClientIP(r *http.Request) string {
	peer, _ := r.Context().Value(peerCtxKey{}).(string)
	if peer == "" {
		peer = r.RemoteAddr
	}
	host := hostOnly(peer)
	if a, err := netip.ParseAddr(host); err == nil && a.IsLoopback() {
		return hostOnly(r.RemoteAddr)
	}
	return host
}

func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// ipAllowed — адрес в списке токена (пустой список — любой).
func ipAllowed(list []string, ip string) bool {
	if len(list) == 0 {
		return true
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, item := range list {
		if p, err := netip.ParsePrefix(item); err == nil && p.Contains(a) {
			return true
		}
		if b, err := netip.ParseAddr(item); err == nil && b.Unmap() == a {
			return true
		}
	}
	return false
}

// Authenticate — auth.TokenAuthenticator.
func (a *tokenAuth) Authenticate(r *http.Request) (context.Context, error) {
	ip := tokenClientIP(r)
	failKey := "api-token:" + ip
	if !a.fails.Allow(failKey) {
		return nil, tokenDeny(http.StatusTooManyRequests, "auth.tokenTooMany")
	}
	edgeName, byEdge := viaEdge(r.Context())
	// Внутренний вызов MCP: запрос агента уже проверен (подпись, edge).
	t, internal, err := a.mcpInternalToken(r)
	if internal {
		if err != nil {
			return nil, err
		}
		// Дальше (прокси на хост) пометка не уходит.
		r.Header.Del(mcpInternalHeader)
		byEdge = false
	} else {
		if byEdge && r.Header.Get("X-NKT-API-Signature") == "" {
			return nil, tokenDeny(http.StatusUnauthorized, "auth.tokenEdgeSigned")
		}
		t, err = a.verify(r)
	}
	if err != nil {
		var te *auth.TokenError
		if errors.As(err, &te) && te.Code == http.StatusUnauthorized {
			a.fails.Fail(failKey)
		}
		return nil, err
	}
	a.fails.Clear(failKey)
	now := time.Now()
	if byEdge && !t.ViaEdge {
		return nil, tokenDeny(http.StatusForbidden, "auth.tokenEdgeDenied", t.Name, edgeName)
	}
	if t.Expired(now) {
		return nil, tokenDeny(http.StatusUnauthorized, "auth.tokenExpired", t.Name)
	}
	if !ipAllowed(t.IPs, ip) {
		return nil, tokenDeny(http.StatusForbidden, "auth.tokenIPDenied", ip)
	}
	ctx := r.Context()
	if last, err := time.Parse(time.RFC3339, t.LastUsed); err != nil || now.Sub(last) > tokenTouchGap || t.LastIP != ip {
		_ = a.s.db.TouchAPIToken(ctx, t.ID, ip)
	}
	role := store.RoleViewer
	if t.Role == store.TokenRoleAdmin {
		role = store.RoleAdmin
	}
	ctx = auth.WithUser(ctx, store.User{Username: "token:" + t.Name, Role: role})
	ctx = context.WithValue(ctx, tokenCtxKey{}, t)
	grant, act, err := a.s.tokenPolicy(r.WithContext(ctx), t)
	if err != nil {
		return nil, err
	}
	if act {
		// Токен «по правам»: маршрут и действие уже проверены политикой —
		// этот запрос проходит RequireAdmin.
		ctx = auth.WithUser(ctx, store.User{Username: "token:" + t.Name, Role: store.RoleAdmin})
	}
	if grant {
		ctx = auth.WithReadGrant(ctx)
	}
	return ctx, nil
}

// verify — токен по заголовкам: Bearer или подпись.
func (a *tokenAuth) verify(r *http.Request) (store.APIToken, error) {
	bad := tokenDeny(http.StatusUnauthorized, "auth.tokenInvalid")
	if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		raw = strings.TrimSpace(raw)
		rest, ok := strings.CutPrefix(raw, tokenPrefix)
		if !ok || len(rest) < tokenKeyLen+2 || rest[tokenKeyLen] != '_' {
			return store.APIToken{}, bad
		}
		keyID, secret := rest[:tokenKeyLen], rest[tokenKeyLen+1:]
		t, plain, err := a.load(r.Context(), keyID)
		if err != nil {
			return t, bad
		}
		if subtle.ConstantTimeCompare([]byte(secret), []byte(plain)) != 1 {
			return t, bad
		}
		return t, nil
	}
	keyID := r.Header.Get("X-NKT-API-Key")
	ts, nonce, sig := r.Header.Get("X-NKT-API-Timestamp"), r.Header.Get("X-NKT-API-Nonce"), r.Header.Get("X-NKT-API-Signature")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || !tokenNonceRe.MatchString(nonce) || len(sig) != 64 {
		return store.APIToken{}, bad
	}
	now := time.Now()
	if d := now.Sub(time.Unix(sec, 0)); d > tokenSkew || d < -tokenSkew {
		return store.APIToken{}, tokenDeny(http.StatusUnauthorized, "auth.tokenStale")
	}
	t, plain, err := a.load(r.Context(), keyID)
	if err != nil {
		return t, bad
	}
	var body []byte
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, tokenMaxBody+1))
		if err != nil {
			return t, bad
		}
		if len(body) > tokenMaxBody {
			return t, tokenDeny(http.StatusRequestEntityTooLarge, "auth.tokenBodyTooLarge")
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	want := TokenSignature(plain, ts, nonce, r.Method, r.URL.RequestURI(), body)
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(want)) {
		return t, bad
	}
	if !a.useNonce(keyID+":"+nonce, now) {
		return t, tokenDeny(http.StatusUnauthorized, "auth.tokenReplay")
	}
	return t, nil
}

func (a *tokenAuth) load(ctx context.Context, keyID string) (store.APIToken, string, error) {
	if !tokenKeyRe.MatchString(keyID) {
		return store.APIToken{}, "", store.ErrNotFound
	}
	t, err := a.s.db.APITokenByKeyID(ctx, keyID)
	if err != nil {
		return t, "", err
	}
	plain, err := secretbox.Decrypt(a.s.hub.key, t.SecretEnc)
	if err != nil {
		return t, "", err
	}
	return t, string(plain), nil
}

// --- что токену можно -----------------------------------------------------

// tokenHostRead — разделы хоста, которые читает токен с ролью «чтение».
var tokenHostRead = map[string]bool{
	"overview": true, "inventory": true, "findings": true, "topology": true, "health": true,
	"services": true, "containers": true, "podman": true, "lxd": true, "vms": true, "guests": true,
	"compose": true, "images": true, "vulnerabilities": true, "certificates": true, "monitor": true,
	"jobs": true, "fail2ban": true, "firewall": true, "updates": true, "interfaces": true,
	"network": true, "net": true, "disks": true, "hardware": true, "sites": true, "changes": true,
	"malware": true, "clamav": true,
}

// tokenHostDeny — разделы хоста, закрытые любому токену: терминал и
// консоль, файлы и бэкапы, учётки, Kubernetes (kubeconfig, exec),
// содержимое конфигов.
var tokenHostDeny = map[string]bool{
	"terminal": true, "console": true, "files": true, "backups": true, "auth": true, "users": true,
	"os-users": true, "ui": true, "k8s": true, "configs": true,
	// Описания сервисов: образец читает файлы и команды хоста.
	"svcdesc": true,
}

// tokenHubRoutes — вызовы хаба, открытые токену: маршрут → какое действие
// нужно («» — хватает чтения). Администратору можно всё, токену «по
// правам» — перечисленное в Perms.Actions. Хосты (/hosts/…) — отдельно
// (tokenHostAction).
var tokenHubRoutes = map[string]string{
	"GET /auth/me":                        "",
	"GET /hub/version":                    "",
	"GET /hub/hosts":                      "",
	"GET /hub/events":                     "",
	"GET /hub/fail2ban/banned":            "",
	"GET /hub/sites":                      "",
	"GET /hub/pipelines":                  "",
	"GET /hub/pipelines/{id}":             "",
	"GET /hub/pipelines/{id}/deployments": "",
	"GET /hub/pipelines/examples":         "",
	// Создать, править и удалить конвейер: токену «по правам» — только
	// compose на своих хостах и только свои (созданные им).
	"POST /hub/pipelines":                   store.TokenActPipelineCreate,
	"PUT /hub/pipelines/{id}":               store.TokenActPipelineCreate,
	"POST /hub/pipelines/{id}/remove":       store.TokenActPipelineCreate,
	"GET /hub/jobs/{id}":                    "",
	"GET /hub/jobs/{id}/log":                "",
	"POST /hub/pipelines/{id}/deploy":       store.TokenActPipelineDeploy,
	"POST /hub/pipelines/{id}/rollback":     store.TokenActPipelineDeploy,
	"GET /hub/pipelines/{id}/power":         "",
	"POST /hub/pipelines/{id}/start":        store.TokenActPipelinePower,
	"POST /hub/pipelines/{id}/stop":         store.TokenActPipelinePower,
	"POST /hub/pipelines/dryrun":            store.TokenActPipelineDeploy,
	"POST /hub/fail2ban/fleet":              store.TokenActFail2ban,
	"GET /hosts/{id}/vulnerabilities":       "",
	"POST /hosts/{id}/vulnerabilities/scan": store.TokenActVulnScan,
	// Сценарии: читать и запускать сохранённые; создавать и править — только
	// в интерфейсе (с историей версий).
	"GET /hub/scripts":           "",
	"GET /hub/scripts/{id}":      "",
	"POST /hub/scripts/check":    store.TokenActScriptRun,
	"POST /hub/scripts/{id}/run": store.TokenActScriptRun,
	// Переносы стеков.
	"GET /hub/migrations":                "",
	"GET /hub/migrate/live":              "",
	"POST /hub/migrate/preview":          store.TokenActMigrate,
	"POST /hub/migrate":                  store.TokenActMigrate,
	"POST /hub/migrations/{id}/{action}": store.TokenActMigrate,
	// Traffic Fly — карта общая на все хосты: только токену без предела
	// (tokenPolicy).
	"GET /hub/fly":                     "",
	"GET /hub/fly/status":              "",
	"GET /hub/fly/versions":            "",
	"PUT /hub/fly":                     store.TokenActFly,
	"POST /hub/fly/ops":                store.TokenActFly,
	"POST /hub/fly/routes/{id}/switch": store.TokenActFly,
	"POST /hub/fly/apply":              store.TokenActFly,
	// MCP: инструменты — внутренние вызовы этого же API под тем же
	// токеном (mcp.go), права — их.
	"POST /mcp": "",
	// Плагины: список и операции; что меняет — с действием plugin.<имя>
	// (проверяет handlePluginCall по манифесту).
	"GET /hub/plugins":               "",
	"GET /hub/plugins/{name}/agents": "",
	// Хосты плагинов: смотреть; действия и команды — с действием
	// plugin.<имя> (проверяет обработчик), хост — в пределах токена.
	"GET /hub/plugin-hosts/types":         "",
	"GET /hub/plugin-hosts/{id}":          "",
	"GET /hub/plugin-hosts/{id}/snapshot": "",
	"GET /hub/plugin-hosts/{id}/findings": "",
	"GET /hub/plugin-hosts/{id}/metrics":  "",
	"GET /hub/plugin-hosts/{id}/config":   "",
	"POST /hub/plugin-hosts/{id}/action":  "",
	"POST /hub/plugin-hosts/{id}/exec":    "",
	"POST /hub/plugins/{name}/call":       "",
	// Кластеры: смотреть и применять манифесты; создание, удаление,
	// обновление, kubeconfig и Helm — нет.
	"GET /hub/clusters":             "",
	"GET /hub/clusters/{id}/nodes":  "",
	"GET /hub/k8s/manifests":        store.TokenActK8sApply,
	"GET /hub/k8s/manifests/{id}":   store.TokenActK8sApply,
	"POST /hub/k8s/manifests/diff":  store.TokenActK8sApply,
	"POST /hub/k8s/manifests/apply": store.TokenActK8sApply,
	// Машины на хостах: создать, включить, выключить, снимки, ресурсы,
	// переустановка, удаление — токену «по правам» с потолками; только
	// свои машины (учёт токена).
	"POST /hub/vm/provision":                          store.TokenActVMManage,
	"POST /hub/hosts/{id}/vm/{action}":                store.TokenActVMManage,
	"GET /hub/hosts/{id}/vm/snapshots":                store.TokenActVMManage,
	"POST /hub/hosts/{id}/vm/snapshots":               store.TokenActVMManage,
	"POST /hub/hosts/{id}/vm/snapshots/{snap}/revert": store.TokenActVMManage,
	"DELETE /hub/hosts/{id}/vm/snapshots/{snap}":      store.TokenActVMManage,
	"POST /hub/hosts/{id}/vm/resize":                  store.TokenActVMManage,
	"POST /hub/hosts/{id}/vm/reinstall":               store.TokenActVMManage,
	"DELETE /hub/hosts/{id}/vm":                       store.TokenActVMManage,
	// Экземпляры LXD — как машины: свои, в потолках, в сети токена.
	"GET /hub/hosts/{id}/lxd":                                  store.TokenActVMManage,
	"POST /hub/hosts/{id}/lxd":                                 store.TokenActVMManage,
	"POST /hub/hosts/{id}/lxd/{name}/{action}":                 store.TokenActVMManage,
	"GET /hub/hosts/{id}/lxd/{name}/snapshots":                 store.TokenActVMManage,
	"POST /hub/hosts/{id}/lxd/{name}/snapshots":                store.TokenActVMManage,
	"POST /hub/hosts/{id}/lxd/{name}/snapshots/{snap}/restore": store.TokenActVMManage,
	"DELETE /hub/hosts/{id}/lxd/{name}/snapshots/{snap}":       store.TokenActVMManage,
	"POST /hub/hosts/{id}/lxd/{name}/resize":                   store.TokenActVMManage,
	"DELETE /hub/hosts/{id}/lxd/{name}":                        store.TokenActVMManage,
	// Свои namespace в общих кластерах: завести, kubeconfig, логи, helm.
	"GET /hub/k8s/own":                            store.TokenActK8sNamespace,
	"POST /hub/k8s/own":                           store.TokenActK8sNamespace,
	"POST /hub/k8s/own/{cluster}/{ns}/kubeconfig": store.TokenActK8sNamespace,
	"DELETE /hub/k8s/own/{cluster}/{ns}":          store.TokenActK8sNamespace,
	"GET /hub/k8s/own/{cluster}/{ns}/logs":        store.TokenActK8sNamespace,
	"POST /hub/k8s/own/{cluster}/{ns}/helm":       store.TokenActK8sNamespace,
	// Порты из пула хоста на свои машины; свои маршруты Traffic Fly.
	"GET /hub/hosts/{id}/vm/ports":           store.TokenActVMPorts,
	"POST /hub/hosts/{id}/vm/ports":          store.TokenActVMPorts,
	"DELETE /hub/hosts/{id}/vm/ports/{port}": store.TokenActVMPorts,
	"GET /hub/own/fly":                       store.TokenActFlyOwn,
	"POST /hub/own/fly":                      store.TokenActFlyOwn,
	"DELETE /hub/own/fly/{route}":            store.TokenActFlyOwn,
}

// tokenHostAction — запись в разделы хоста через прокси: какое действие
// нужно («-» — закрыто токену «по правам», только администратору).
func tokenHostAction(method, rest string) string {
	section, tail, _ := strings.Cut(rest, "/")
	parts := strings.Split(tail, "/")
	switch {
	case (section == "containers" || section == "podman") && method == http.MethodPost && len(parts) == 2:
		return store.TokenActContainer
	case section == "services" && method == http.MethodPost && len(parts) == 2:
		return store.TokenActService
	case section == "vms" && (method == http.MethodPost && len(parts) == 2 || method == http.MethodDelete && len(parts) == 1):
		return store.TokenActVMManage
	}
	return "-"
}

// tokenRouteAction — какое действие нужно для маршрута (для MCP: какие
// инструменты показать токену). known — маршрут токену вообще открыт.
func tokenRouteAction(method, pattern string) (action string, known bool) {
	if act, ok := tokenHubRoutes[method+" "+pattern]; ok {
		return act, true
	}
	for _, prefix := range []string{"/hosts/{id}/", "/hosts/local/"} {
		if rest, ok := strings.CutPrefix(pattern, prefix); ok {
			if method == http.MethodGet {
				section, _, _ := strings.Cut(rest, "/")
				return "", tokenHostRead[section]
			}
			act := tokenHostAction(method, rest)
			return act, act != "-"
		}
	}
	return "", false
}

// tokenPolicy — открыт ли токену этот вызов. grant — GET, открытый
// токену чтения за RequireAdmin; act — действие токена «по правам»
// разрешено: этот запрос проходит как администраторский.
func (s *Server) tokenPolicy(r *http.Request, t store.APIToken) (grant, act bool, err error) {
	deny := tokenDeny(http.StatusForbidden, "auth.tokenRouteDenied", r.Method, r.URL.Path)
	if r.Header.Get("Upgrade") != "" {
		return false, false, deny
	}
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return false, false, deny
	}
	pattern := strings.TrimPrefix(rctx.RoutePattern(), "/api")
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	need := func(action string) error {
		if action == "" || t.Can(action) {
			return nil
		}
		if t.Role == store.TokenRoleCustom {
			return tokenDeny(http.StatusForbidden, "auth.tokenActionDenied", action)
		}
		return tokenDeny(http.StatusForbidden, "auth.tokenReadOnly")
	}
	if action, ok := tokenHubRoutes[method+" "+pattern]; ok {
		if err := need(action); err != nil {
			return false, false, err
		}
		if strings.HasPrefix(pattern, "/hub/fly") && t.Scoped() {
			return false, false, tokenDeny(http.StatusForbidden, "auth.tokenFlyScoped")
		}
		if (strings.HasPrefix(pattern, "/hosts/{id}/") || strings.HasPrefix(pattern, "/hub/hosts/{id}/")) && !s.tokenHostAllowed(r.Context(), t, chi.URLParam(r, "id")) {
			return false, false, tokenDeny(http.StatusForbidden, "auth.tokenHostDenied")
		}
		if t.Limited() && action != "" {
			if err := s.tokenObjectAllowed(r, t, pattern); err != nil {
				return false, false, err
			}
			if !read {
				if err := s.tokenRate(t, action); err != nil {
					return false, false, err
				}
			}
		}
		return read, action != "" && t.Limited(), nil
	}
	var hostRef string
	switch pattern {
	case "/hosts/{id}/*":
		hostRef = chi.URLParam(r, "id")
	case "/hosts/local/*":
		hostRef = "local"
	default:
		return false, false, deny
	}
	rest := chi.URLParam(r, "*")
	section, _, _ := strings.Cut(rest, "/")
	if tokenHostDeny[section] {
		return false, false, deny
	}
	admin := t.Role == store.TokenRoleAdmin
	if read && !admin && !tokenHostRead[section] {
		return false, false, deny
	}
	if !s.tokenHostAllowed(r.Context(), t, hostRef) {
		return false, false, tokenDeny(http.StatusForbidden, "auth.tokenHostDenied")
	}
	if read || admin {
		return read, false, nil
	}
	action := tokenHostAction(method, rest)
	if action == "-" {
		return false, false, tokenDeny(http.StatusForbidden, "auth.tokenReadOnly")
	}
	if err := need(action); err != nil {
		return false, false, err
	}
	if action == store.TokenActVMManage {
		// Машина — только своя: та, что токен создал (учёт).
		if err := s.tokenOwnsVM(r.Context(), t, hostRef, strings.Split(rest, "/")[1]); err != nil {
			return false, false, err
		}
	}
	return false, true, nil
}

// hostGroups — группа каждого хоста (у машины — группа её хоста) и
// машины хаба.
func (s *Server) hostGroups(ctx context.Context) map[int64]string { return s.hub.hostGroups(ctx) }

func (m *Manager) hostGroups(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	hosts, err := m.db.ListAllHosts(ctx)
	if err != nil {
		return out
	}
	for _, h := range hosts {
		out[h.ID] = h.Group
	}
	for _, h := range hosts {
		if h.ParentID != 0 {
			if g, ok := out[h.ParentID]; ok {
				out[h.ID] = g
			}
		}
	}
	out[localHostID] = m.LocalHostGroup(ctx)
	return out
}

func (s *Server) tokenHostAllowed(ctx context.Context, t store.APIToken, ref string) bool {
	if !t.Scoped() && !t.Limited() {
		return true
	}
	id := int64(localHostID)
	if ref != "local" {
		var err error
		if id, err = strconv.ParseInt(ref, 10, 64); err != nil {
			return false
		}
	}
	return s.scopeFilter(withToken(ctx, t))(id)
}

func withToken(ctx context.Context, t store.APIToken) context.Context {
	return context.WithValue(ctx, tokenCtxKey{}, t)
}

// scopeFilter — фильтр хостов для списков (нет токена или он без
// пределов — nil, показывать всё).
//
// Машины (дочерние хосты) токену «по правам» — только свои: чужие (других
// токенов и администратора) он не видит и не трогает ни в списках, ни в
// мониторинге, ни в сценариях. Свои машины доступны и токену с пределом по
// хостам, даже если сам хост машины в его списке не значится.
func (s *Server) scopeFilter(ctx context.Context) func(id int64) bool {
	t, ok := tokenFromContext(ctx)
	if !ok || (!t.Scoped() && !t.Limited()) {
		return nil
	}
	groups := s.hostGroups(ctx)
	vms, owned := s.tokenVMHosts(ctx, t)
	return func(id int64) bool {
		switch {
		case owned[id]:
			return true
		case t.Limited() && vms[id]:
			return false
		}
		return !t.Scoped() || t.AllowsHost(id, groups[id])
	}
}

// tokenVMHosts — все машины-хосты хаба (дочерние) и те из них, что
// принадлежат токену (учёт token_usage: хост-родитель и имя машины).
func (s *Server) tokenVMHosts(ctx context.Context, t store.APIToken) (vms, owned map[int64]bool) {
	vms, owned = map[int64]bool{}, map[int64]bool{}
	hosts, err := s.db.ListAllHosts(ctx)
	if err != nil {
		return vms, owned
	}
	mine := map[string]bool{}
	if usage, err := s.db.ListTokenUsage(ctx, t.ID); err == nil {
		for _, u := range usage {
			if u.Kind == store.UsageVM {
				mine[strconv.FormatInt(u.HostID, 10)+"/"+u.Ref] = true
			}
		}
	}
	for _, h := range hosts {
		if h.ParentID == 0 {
			continue
		}
		vms[h.ID] = true
		if mine[strconv.FormatInt(h.ParentID, 10)+"/"+h.Name] {
			owned[h.ID] = true
		}
	}
	return vms, owned
}

// pipelineInScope — конвейер целиком в пределах токена: compose-стек, все
// хосты которого токену доступны. Остальные виды (кластеры, сценарий
// хаба) токену с пределами недоступны.
func (s *Server) pipelineInScope(ctx context.Context, p store.Pipeline) bool {
	allow := s.scopeFilter(ctx)
	if allow == nil {
		return true
	}
	targets, ok := s.pipelineTargets(ctx, p)
	if !ok {
		// Не compose (кластеры, сценарий хаба): токену с пределом по хостам
		// — нет; без него — как раньше (доступ решает список конвейеров).
		t, _ := tokenFromContext(ctx)
		return !t.Scoped()
	}
	for _, t := range targets {
		if !allow(t.ID) {
			return false
		}
	}
	return true
}

// pipelineTargets — хосты compose-конвейера (прочие виды — нет).
func (s *Server) pipelineTargets(ctx context.Context, p store.Pipeline) ([]targetHost, bool) {
	spec, err := deploy.ParseSpec(p.Content)
	if err != nil || spec.Action != deploy.ActionCompose || spec.Compose == nil {
		return nil, false
	}
	targets, err := s.resolveHosts(ctx, spec.Compose.Hosts, spec.Compose.Group)
	if err != nil || len(targets) == 0 {
		return nil, false
	}
	return targets, true
}
