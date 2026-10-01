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
	if byEdge && r.Header.Get("X-NKT-API-Signature") == "" {
		return nil, tokenDeny(http.StatusUnauthorized, "auth.tokenEdgeSigned")
	}
	t, err := a.verify(r)
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
	grant, err := a.s.tokenPolicy(r.WithContext(ctx), t)
	if err != nil {
		return nil, err
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
}

// tokenHubRoutes — вызовы хаба, открытые токену: маршрут → нужна ли роль
// администратора. Хосты (/hosts/…) — отдельно.
var tokenHubRoutes = map[string]bool{
	"GET /auth/me":                          false,
	"GET /hub/version":                      false,
	"GET /hub/hosts":                        false,
	"GET /hub/events":                       false,
	"GET /hub/fail2ban/banned":              false,
	"GET /hub/sites":                        false,
	"GET /hub/pipelines":                    false,
	"GET /hub/pipelines/{id}":               false,
	"GET /hub/pipelines/{id}/deployments":   false,
	"GET /hub/jobs/{id}":                    false,
	"GET /hub/jobs/{id}/log":                false,
	"POST /hub/pipelines/{id}/deploy":       true,
	"POST /hub/pipelines/{id}/rollback":     true,
	"POST /hub/pipelines/dryrun":            true,
	"POST /hub/fail2ban/fleet":              true,
	"GET /hosts/{id}/vulnerabilities":       false,
	"POST /hosts/{id}/vulnerabilities/scan": true,
}

// tokenPolicy — открыт ли токену этот вызов. grant — GET, открытый
// токену чтения за RequireAdmin.
func (s *Server) tokenPolicy(r *http.Request, t store.APIToken) (bool, error) {
	deny := tokenDeny(http.StatusForbidden, "auth.tokenRouteDenied", r.Method, r.URL.Path)
	if r.Header.Get("Upgrade") != "" {
		return false, deny
	}
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return false, deny
	}
	pattern := strings.TrimPrefix(rctx.RoutePattern(), "/api")
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	admin := t.Role == store.TokenRoleAdmin
	if needAdmin, ok := tokenHubRoutes[method+" "+pattern]; ok {
		if needAdmin && !admin {
			return false, tokenDeny(http.StatusForbidden, "auth.tokenReadOnly")
		}
		if strings.HasPrefix(pattern, "/hosts/{id}/") && !s.tokenHostAllowed(r.Context(), t, chi.URLParam(r, "id")) {
			return false, tokenDeny(http.StatusForbidden, "auth.tokenHostDenied")
		}
		return read, nil
	}
	var hostRef string
	switch pattern {
	case "/hosts/{id}/*":
		hostRef = chi.URLParam(r, "id")
	case "/hosts/local/*":
		hostRef = "local"
	default:
		return false, deny
	}
	section, _, _ := strings.Cut(chi.URLParam(r, "*"), "/")
	if tokenHostDeny[section] {
		return false, deny
	}
	if !read && !admin {
		return false, tokenDeny(http.StatusForbidden, "auth.tokenReadOnly")
	}
	if read && !admin && !tokenHostRead[section] {
		return false, deny
	}
	if !s.tokenHostAllowed(r.Context(), t, hostRef) {
		return false, tokenDeny(http.StatusForbidden, "auth.tokenHostDenied")
	}
	return read, nil
}

// hostGroups — группа каждого хоста (у машины — группа её хоста) и
// машины хаба.
func (s *Server) hostGroups(ctx context.Context) map[int64]string {
	out := map[int64]string{}
	hosts, err := s.db.ListHosts(ctx)
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
	out[localHostID] = s.hub.LocalHostGroup(ctx)
	return out
}

func (s *Server) tokenHostAllowed(ctx context.Context, t store.APIToken, ref string) bool {
	if !t.Scoped() {
		return true
	}
	id := int64(localHostID)
	if ref != "local" {
		var err error
		if id, err = strconv.ParseInt(ref, 10, 64); err != nil {
			return false
		}
	}
	return t.AllowsHost(id, s.hostGroups(ctx)[id])
}

// scopeFilter — фильтр хостов для списков (нет токена или он без
// пределов — nil, показывать всё).
func (s *Server) scopeFilter(ctx context.Context) func(id int64) bool {
	t, ok := tokenFromContext(ctx)
	if !ok || !t.Scoped() {
		return nil
	}
	groups := s.hostGroups(ctx)
	return func(id int64) bool { return t.AllowsHost(id, groups[id]) }
}

// pipelineInScope — конвейер целиком в пределах токена: compose-стек, все
// хосты которого токену доступны. Остальные виды (кластеры, сценарий
// хаба) токену с пределами недоступны.
func (s *Server) pipelineInScope(ctx context.Context, p store.Pipeline) bool {
	allow := s.scopeFilter(ctx)
	if allow == nil {
		return true
	}
	spec, err := deploy.ParseSpec(p.Content)
	if err != nil || spec.Action != deploy.ActionCompose || spec.Compose == nil {
		return false
	}
	targets, err := s.resolveHosts(ctx, spec.Compose.Hosts, spec.Compose.Group)
	if err != nil || len(targets) == 0 {
		return false
	}
	for _, t := range targets {
		if !allow(t.ID) {
			return false
		}
	}
	return true
}
