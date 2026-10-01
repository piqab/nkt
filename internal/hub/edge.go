package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hashicorp/yamux"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/edge"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// nkt-edge со стороны хаба: хаб сам подключается к каждому edge на VPS
// (TLS с закреплённым отпечатком, токен, yamux) и держит соединения. У
// edge роли — что он принимает из интернета и передаёт хабу:
//
//   - hooks — вебхуки выкладок, POST /hooks/{hook};
//   - api — подписанные запросы API-токенов к /api/hub/… и /api/hosts/…
//     (Bearer, cookie и веб-сокеты не проходят: секрет на VPS не попадает).
//
// Хаб обслуживает по туннелю только роли этого edge — даже если сам edge
// пропустит лишнее. Edge может быть несколько (разные VPS и имена: вебхуки
// на одном, API на другом). Настройки — список в KV хаба, токены туннелей
// зашифрованы.

const (
	// edgeSettingsKey — до v1.11.71 edge был один; при первом чтении он
	// переезжает в список.
	edgeSettingsKey = "hub.edge"
	edgesKey        = "hub.edges"
)

// Роли edge.
const (
	EdgeRoleHooks = "hooks"
	EdgeRoleAPI   = "api"
	// EdgeRoleProbe — проверки «снаружи» по просьбе хаба (DNS, порты,
	// HTTPS): из интернета не принимает ничего, только по туннелю.
	EdgeRoleProbe = "probe"
)

// EdgeRoles — все роли, в порядке показа.
var EdgeRoles = []string{EdgeRoleHooks, EdgeRoleAPI, EdgeRoleProbe}

// EdgeSettings — один edge.
type EdgeSettings struct {
	ID      int64 `json:"id"`
	Enabled bool  `json:"enabled"`
	// Address — куда подключаться (хост:8444).
	Address string `json:"address"`
	// Domain — имя с сертификатом Let's Encrypt (адрес вебхуков и API).
	Domain   string `json:"domain"`
	TokenEnc []byte `json:"token_enc,omitempty"`
	// CertPEM — сертификат туннеля edge: хаб доверяет ровно ему.
	CertPEM     string `json:"cert_pem,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// HostID — хост хаба, на который edge поставлен (0 — вручную).
	HostID int64 `json:"host_id,omitempty"`
	// Roles — что edge передаёт хабу; пусто — только вебхуки (как было).
	Roles []string `json:"roles,omitempty"`
}

// Has — у edge есть роль.
func (st EdgeSettings) Has(role string) bool {
	if len(st.Roles) == 0 {
		return role == EdgeRoleHooks
	}
	return slices.Contains(st.Roles, role)
}

func (st EdgeSettings) roles() []string {
	if len(st.Roles) == 0 {
		return []string{EdgeRoleHooks}
	}
	return st.Roles
}

// validRoles — роли из запроса: известные, без повторов, хоть одна.
func validRoles(in []string) ([]string, bool) {
	var out []string
	for _, r := range in {
		if !slices.Contains(EdgeRoles, r) {
			return nil, false
		}
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return []string{EdgeRoleHooks}, true
	}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(EdgeRoles, a) - slices.Index(EdgeRoles, b) })
	return out, true
}

// edgesMu — правка списка edge (KV): хаб и импорт.
var edgesMu sync.Mutex

// loadEdges — все edge хаба (прежний одиночный переезжает в список).
func loadEdges(ctx context.Context, db *store.DB) []EdgeSettings {
	var list []EdgeSettings
	if raw, ok, err := db.KVGet(ctx, edgesKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &list)
		return list
	}
	var old EdgeSettings
	if raw, ok, err := db.KVGet(ctx, edgeSettingsKey); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &old)
	}
	if old.Address != "" || len(old.TokenEnc) > 0 {
		old.ID, old.Roles = 1, []string{EdgeRoleHooks}
		list = append(list, old)
	}
	edgesMu.Lock()
	defer edgesMu.Unlock()
	if saveEdgesLocked(ctx, db, list) == nil {
		_ = db.KVSet(ctx, edgeSettingsKey, "{}")
	}
	return list
}

func saveEdgesLocked(ctx context.Context, db *store.DB, list []EdgeSettings) error {
	if list == nil {
		list = []EdgeSettings{}
	}
	b, _ := json.Marshal(list)
	return db.KVSet(ctx, edgesKey, string(b))
}

// putEdgeDB — сохранить edge (новый — с новым номером).
func putEdgeDB(ctx context.Context, db *store.DB, st EdgeSettings) (EdgeSettings, error) {
	list := loadEdges(ctx, db)
	edgesMu.Lock()
	defer edgesMu.Unlock()
	if st.ID == 0 {
		for _, e := range list {
			st.ID = max(st.ID, e.ID)
		}
		st.ID++
		list = append(list, st)
	} else {
		i := slices.IndexFunc(list, func(e EdgeSettings) bool { return e.ID == st.ID })
		if i < 0 {
			list = append(list, st)
		} else {
			list[i] = st
		}
	}
	return st, saveEdgesLocked(ctx, db, list)
}

func (s *Server) edges(ctx context.Context) []EdgeSettings { return loadEdges(ctx, s.db) }

func (s *Server) edgeByID(ctx context.Context, id int64) (EdgeSettings, bool) {
	for _, st := range s.edges(ctx) {
		if st.ID == id {
			return st, true
		}
	}
	return EdgeSettings{}, false
}

// putEdge — сохранить edge (новый — с новым номером).
func (s *Server) putEdge(ctx context.Context, st EdgeSettings) (EdgeSettings, error) {
	return putEdgeDB(ctx, s.db, st)
}

// dropEdge — хаб забывает edge (на VPS он остаётся).
func (s *Server) dropEdge(ctx context.Context, id int64) error {
	list := s.edges(ctx)
	edgesMu.Lock()
	defer edgesMu.Unlock()
	return saveEdgesLocked(ctx, s.db, slices.DeleteFunc(list, func(e EdgeSettings) bool { return e.ID == id }))
}

// --- соединения -------------------------------------------------------------

type edgeHub struct {
	mu      sync.Mutex
	clients map[int64]*edgeClient
	reload  chan struct{}
}

type edgeClient struct {
	mu        sync.Mutex
	connected bool
	since     time.Time
	lastErr   error
	// sig — отпечаток настроек: сменились — переподключение.
	sig    string
	cancel context.CancelFunc
	// sess — открытое соединение (роль probe: хаб открывает потоки сам).
	sess *yamux.Session
}

func edgeSig(st EdgeSettings) string {
	b, _ := json.Marshal(st)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (c *edgeClient) set(connected bool, lastErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if connected && !c.connected {
		c.since = time.Now()
	}
	c.connected = connected
	if lastErr != nil || connected {
		c.lastErr = lastErr
	}
}

// kickEdge — сверить соединения с настройками сейчас.
func (s *Server) kickEdge() {
	if s.edge == nil {
		return
	}
	select {
	case s.edge.reload <- struct{}{}:
	default:
	}
}

// StartEdge держит соединения со всеми включёнными edge.
func (s *Server) StartEdge(ctx context.Context) {
	s.edge = &edgeHub{clients: map[int64]*edgeClient{}, reload: make(chan struct{}, 1)}
	go func() {
		for {
			s.reconcileEdges(ctx)
			select {
			case <-ctx.Done():
				return
			case <-s.edge.reload:
			case <-time.After(time.Minute):
			}
		}
	}()
}

// reconcileEdges — по соединению на включённый edge; сменились настройки
// — переподключиться, edge убран или выключен — отключиться.
func (s *Server) reconcileEdges(ctx context.Context) {
	want := map[int64]EdgeSettings{}
	for _, st := range s.edges(ctx) {
		if st.Enabled && st.Address != "" {
			want[st.ID] = st
		}
	}
	h := s.edge
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, c := range h.clients {
		if st, ok := want[id]; !ok || edgeSig(st) != c.sig {
			c.cancel()
			delete(h.clients, id)
		}
	}
	for id, st := range want {
		if _, ok := h.clients[id]; ok {
			continue
		}
		cctx, cancel := context.WithCancel(ctx)
		c := &edgeClient{sig: edgeSig(st), cancel: cancel}
		h.clients[id] = c
		go s.edgeLoop(cctx, st, c)
	}
}

func (s *Server) edgeLoop(ctx context.Context, st EdgeSettings, c *edgeClient) {
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		err := s.runEdge(ctx, st, c)
		c.set(false, err)
		if err == nil {
			backoff = 5 * time.Second
		} else if backoff < time.Minute {
			backoff *= 2
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
	}
}

// edgeState — состояние соединения с edge (нет соединения — не подключён).
func (s *Server) edgeState(id int64) (connected bool, since time.Time, lastErr error) {
	if s.edge == nil {
		return false, time.Time{}, nil
	}
	s.edge.mu.Lock()
	c := s.edge.clients[id]
	s.edge.mu.Unlock()
	if c == nil {
		return false, time.Time{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected, c.since, c.lastErr
}

// runEdge — одно подключение: до разрыва или смены настроек.
func (s *Server) runEdge(ctx context.Context, st EdgeSettings, c *edgeClient) error {
	token, err := secretbox.Decrypt(s.hub.key, st.TokenEnc)
	if err != nil {
		return err
	}
	if st.CertPEM == "" {
		return msgs.Errorf("edge.noCert")
	}
	conn, _, err := edge.Dial(st.Address, string(token), st.CertPEM, 15*time.Second)
	if err != nil {
		if errors.Is(err, io.EOF) {
			// Порт туннеля принял соединение и закрыл его без ответа:
			// обычно служба edge падает и перезапускается по кругу.
			return msgs.Errorf("edge.closedByEdge", st.Address)
		}
		return err
	}
	sess, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		conn.Close()
		return err
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-cctx.Done()
		sess.Close()
	}()
	c.mu.Lock()
	c.sess = sess
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.sess = nil
		c.mu.Unlock()
	}()
	c.set(true, nil)
	srv := &http.Server{Handler: s.edgeHandler(st), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second}
	err = srv.Serve(sess)
	if cctx.Err() != nil || errors.Is(err, net.ErrClosed) || sess.IsClosed() {
		return nil
	}
	return err
}

// edgeHandler — маршруты туннеля: только роли этого edge.
func (s *Server) edgeHandler(st EdgeSettings) http.Handler {
	r := chi.NewRouter()
	if st.Has(EdgeRoleHooks) {
		r.Post("/hooks/{hook}", func(w http.ResponseWriter, r *http.Request) {
			from := r.Header.Get("X-Forwarded-For")
			if from == "" {
				from = "edge"
			}
			s.serveHook(w, r, from+" (edge)")
		})
	}
	if st.Has(EdgeRoleAPI) {
		r.HandleFunc("/api/*", func(w http.ResponseWriter, r *http.Request) { s.serveEdgeAPI(w, r, st) })
	}
	return r
}

type edgeCtxKey struct{}

// viaEdge — запрос пришёл через edge (роль api): имя edge.
func viaEdge(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(edgeCtxKey{}).(string)
	return v, ok
}

// serveEdgeAPI — запрос токена через edge: только подписанный, без cookie
// и веб-сокетов; адрес клиента — тот, что передал edge (edge проверен
// закреплённым сертификатом и токеном туннеля).
func (s *Server) serveEdgeAPI(w http.ResponseWriter, r *http.Request, st EdgeSettings) {
	lang := msgs.LangFromRequest(r)
	refuse := func(code int, key string) {
		writeError(w, code, msgs.T(lang, key))
	}
	if !edge.APIPath(r.URL.Path) || r.Header.Get("Upgrade") != "" {
		refuse(http.StatusNotFound, "edge.apiPathDenied")
		return
	}
	if r.Header.Get("Authorization") != "" || r.Header.Get("X-NKT-API-Signature") == "" {
		refuse(http.StatusUnauthorized, "auth.tokenEdgeSigned")
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Forwarded-For")))
	if err != nil {
		refuse(http.StatusBadRequest, "edge.apiNoClient")
		return
	}
	r.Header.Del("Cookie")
	r.Header.Del("X-Real-IP")
	r.Header.Set("X-Forwarded-For", ip.String())
	r.RemoteAddr = net.JoinHostPort(ip.String(), "0")
	name := st.Domain
	if name == "" {
		name = st.Address
	}
	s.apiOnce.Do(func() { s.apiHandler = s.Handler() })
	// Свой контекст маршрута у роутера туннеля — API хаба должен
	// разобрать путь заново (см. proxyLocal).
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, (*chi.Context)(nil))
	s.apiHandler.ServeHTTP(w, r.WithContext(context.WithValue(ctx, edgeCtxKey{}, name)))
}

// --- состояние и настройка ----------------------------------------------------

type edgeStatusJSON struct {
	ID          int64    `json:"id"`
	Configured  bool     `json:"configured"`
	Enabled     bool     `json:"enabled"`
	Address     string   `json:"address,omitempty"`
	Domain      string   `json:"domain,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	HostID      int64    `json:"host_id,omitempty"`
	Roles       []string `json:"roles"`
	Connected   bool     `json:"connected"`
	Since       string   `json:"since,omitempty"`
	LastError   string   `json:"last_error,omitempty"`
}

// handleEdges — GET /hub/edges.
func (s *Server) handleEdges(w http.ResponseWriter, r *http.Request) {
	out := []edgeStatusJSON{}
	lang := msgs.LangFromRequest(r)
	for _, st := range s.edges(r.Context()) {
		item := edgeStatusJSON{ID: st.ID, Configured: st.Address != "" && len(st.TokenEnc) > 0 && st.CertPEM != "", Enabled: st.Enabled,
			Address: st.Address, Domain: st.Domain, Fingerprint: st.Fingerprint, HostID: st.HostID, Roles: st.roles()}
		connected, since, lastErr := s.edgeState(st.ID)
		item.Connected = connected
		if connected {
			item.Since = since.UTC().Format(time.RFC3339)
		}
		if lastErr != nil {
			item.LastError = msgs.Localize(lang, lastErr)
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": out, "roles": EdgeRoles})
}

var (
	edgeDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	edgeEmailRe  = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,190}\.[A-Za-z]{2,63}$`)
)

func edgeIDParam(r *http.Request) int64 {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id
}

// handleEdgeSave — POST /hub/edges (новый, вручную) и PUT /hub/edges/{id}
// {enabled, address, domain, token, cert_pem, roles}: edge поставлен не из
// хаба. Пустые token и cert_pem при правке — оставить прежние.
func (s *Server) handleEdgeSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool     `json:"enabled"`
		Address string   `json:"address"`
		Domain  string   `json:"domain"`
		Token   string   `json:"token"`
		CertPEM string   `json:"cert_pem"`
		Roles   []string `json:"roles"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	var st EdgeSettings
	if r.Method == http.MethodPut {
		var ok bool
		if st, ok = s.edgeByID(ctx, edgeIDParam(r)); !ok {
			writeError(w, http.StatusNotFound, msgs.Tc(ctx, "edge.missing", edgeIDParam(r)))
			return
		}
	}
	if req.Address != "" {
		if _, _, err := net.SplitHostPort(req.Address); err != nil {
			writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.badAddress", req.Address))
			return
		}
		st.Address = req.Address
	}
	if req.Domain != "" {
		if !edgeDomainRe.MatchString(strings.ToLower(req.Domain)) {
			writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.badDomain", req.Domain))
			return
		}
		st.Domain = strings.ToLower(req.Domain)
	}
	if req.Token != "" {
		if len(req.Token) < 32 {
			writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.shortToken"))
			return
		}
		enc, err := secretbox.Encrypt(s.hub.key, []byte(req.Token))
		if err != nil {
			writeErr(w, r, http.StatusInternalServerError, err)
			return
		}
		st.TokenEnc = enc
	}
	if strings.TrimSpace(req.CertPEM) != "" {
		fp, err := edge.CertInfo(strings.TrimSpace(req.CertPEM))
		if err != nil {
			writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.badCert"))
			return
		}
		st.CertPEM, st.Fingerprint = strings.TrimSpace(req.CertPEM)+"\n", fp
	}
	roles, ok := validRoles(req.Roles)
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.badRoles", strings.Join(req.Roles, ", ")))
		return
	}
	st.Roles, st.Enabled = roles, req.Enabled
	if st.Address == "" || len(st.TokenEnc) == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.manualIncomplete"))
		return
	}
	st, err := s.putEdge(ctx, st)
	s.db.Audit(ctx, auth.Username(ctx), "edge.update", st.Address, auditOutcome(err), map[string]any{"enabled": st.Enabled, "domain": st.Domain, "roles": st.Roles})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.kickEdge()
	writeJSON(w, http.StatusOK, map[string]any{"id": st.ID})
}

// handleEdgeDelete — DELETE /hub/edges/{id}: забыть edge (на VPS он остаётся).
func (s *Server) handleEdgeDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, ok := s.edgeByID(ctx, edgeIDParam(r))
	if !ok {
		writeError(w, http.StatusNotFound, msgs.Tc(ctx, "edge.missing", edgeIDParam(r)))
		return
	}
	err := s.dropEdge(ctx, st.ID)
	s.db.Audit(ctx, auth.Username(ctx), "edge.delete", st.Address, auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.kickEdge()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// KindEdgeInstall — задание «поставить nkt-edge на хост».
const KindEdgeInstall = "edge.install"

// EdgeInstallParams — вход задания.
type EdgeInstallParams struct {
	HostID     int64  `json:"host_id"`
	Domain     string `json:"domain"`
	Email      string `json:"email"`
	GitHubOnly bool   `json:"github_only"`
	// ProxyPort — edge за обратным прокси на VPS (80 и 443 заняты nginx
	// или Caddy): по HTTP на 127.0.0.1:ProxyPort; 0 — edge сам на 80 и 443.
	ProxyPort int `json:"proxy_port,omitempty"`
	// Roles — роли edge (пусто — вебхуки).
	Roles []string `json:"roles,omitempty"`
}

// handleEdgeInstall — POST /hub/edges/install {host_id, domain, email,
// github_only, proxy_port, roles}. На хост, где edge уже стоит, —
// переустановка с новыми настройками (тот же edge в списке).
func (s *Server) handleEdgeInstall(w http.ResponseWriter, r *http.Request) {
	var req EdgeInstallParams
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !edgeDomainRe.MatchString(req.Domain) {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.badDomain", req.Domain))
		return
	}
	if req.Email != "" && !edgeEmailRe.MatchString(req.Email) {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.badEmail", req.Email))
		return
	}
	if req.ProxyPort != 0 && (req.ProxyPort < 1024 || req.ProxyPort > 65535 || strconv.Itoa(req.ProxyPort) == EdgeTunnelPort) {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.badProxyPort", req.ProxyPort))
		return
	}
	roles, ok := validRoles(req.Roles)
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.badRoles", strings.Join(req.Roles, ", ")))
		return
	}
	req.Roles = roles
	host, err := s.db.HostByID(ctx, req.HostID)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(ctx)
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindEdgeInstall, TitleKey: "edge.jobTitle", TitleArgs: []any{host.Name, req.Domain},
		Queue: fmt.Sprintf("host:%d", host.ID), Author: user, Steps: 5, Params: req,
	})
	s.db.Audit(ctx, user, "edge.install", host.Name, auditOutcome(err), map[string]any{"domain": req.Domain, "roles": req.Roles})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// EdgeInstallRunner ставит nkt-edge на хост по SSH.
type EdgeInstallRunner struct{ s *Server }

// NewEdgeInstallRunner строит исполнителя.
func NewEdgeInstallRunner(s *Server) *EdgeInstallRunner { return &EdgeInstallRunner{s: s} }

// Resumable — да: установка повторяема целиком.
func (r *EdgeInstallRunner) Resumable() bool { return true }

// EdgeTunnelPort — порт туннеля на edge.
const EdgeTunnelPort = "8444"

// KindEdgeUninstall — задание «удалить nkt-edge с хоста».
const KindEdgeUninstall = "edge.uninstall"

// EdgeUninstallParams — вход задания: хост, на который edge ставил хаб, и
// сам edge в списке.
type EdgeUninstallParams struct {
	HostID int64 `json:"host_id"`
	EdgeID int64 `json:"edge_id,omitempty"`
}

// handleEdgeUninstall — POST /hub/edges/{id}/uninstall: снять edge с VPS,
// на который его поставил хаб, и забыть его. Edge, поставленный вручную,
// хаб не трогает — только «Забыть».
func (s *Server) handleEdgeUninstall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, ok := s.edgeByID(ctx, edgeIDParam(r))
	if !ok {
		writeError(w, http.StatusNotFound, msgs.Tc(ctx, "edge.missing", edgeIDParam(r)))
		return
	}
	if st.HostID == 0 {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.uninstallManual"))
		return
	}
	host, err := s.db.HostByID(ctx, st.HostID)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(ctx)
	id, err := s.jobs.Start(ctx, jobs.Spec{
		Kind: KindEdgeUninstall, TitleKey: "edge.uninstallTitle", TitleArgs: []any{host.Name},
		Queue: fmt.Sprintf("host:%d", host.ID), Author: user, Steps: 3, Params: EdgeUninstallParams{HostID: host.ID, EdgeID: st.ID},
	})
	s.db.Audit(ctx, user, "edge.uninstall", host.Name, auditOutcome(err), "")
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// EdgeUninstallRunner снимает nkt-edge с хоста по SSH.
type EdgeUninstallRunner struct{ s *Server }

// NewEdgeUninstallRunner строит исполнителя.
func NewEdgeUninstallRunner(s *Server) *EdgeUninstallRunner { return &EdgeUninstallRunner{s: s} }

// Resumable — да: удаление повторяемо.
func (r *EdgeUninstallRunner) Resumable() bool { return true }

// --- роль probe -----------------------------------------------------------------

// probeEdge — подключённый edge с ролью probe: id 0 — любой.
func (s *Server) probeEdge(ctx context.Context, id int64) (EdgeSettings, *yamux.Session, bool) {
	if s.edge == nil {
		return EdgeSettings{}, nil, false
	}
	for _, st := range s.edges(ctx) {
		if (id != 0 && st.ID != id) || !st.Has(EdgeRoleProbe) {
			continue
		}
		s.edge.mu.Lock()
		c := s.edge.clients[st.ID]
		s.edge.mu.Unlock()
		if c == nil {
			continue
		}
		c.mu.Lock()
		sess := c.sess
		c.mu.Unlock()
		if sess != nil && !sess.IsClosed() {
			return st, sess, true
		}
	}
	return EdgeSettings{}, nil, false
}

// edgeName — как edge назвать в журнале.
func edgeName(st EdgeSettings) string {
	if st.Domain != "" {
		return st.Domain
	}
	return st.Address
}

// runProbe — проверки с edge по туннелю.
func runProbe(ctx context.Context, sess *yamux.Session, checks []edge.ProbeCheck) (edge.ProbeResponse, error) {
	var out edge.ProbeResponse
	body, _ := json.Marshal(edge.ProbeRequest{Checks: checks})
	client := &http.Client{
		Timeout: 45 * time.Second,
		Transport: &http.Transport{
			DialContext:       func(context.Context, string, string) (net.Conn, error) { return sess.Open() },
			DisableKeepAlives: true,
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://edge"+edge.ProbePath, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return out, msgs.Errorf("edge.probeFailed", fmt.Sprintf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(msg))))
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	return out, err
}

// handleEdgeProbe — POST /hub/edges/{id}/probe {checks}: проверки «снаружи»
// с этого edge (роль probe).
func (s *Server) handleEdgeProbe(w http.ResponseWriter, r *http.Request) {
	var req edge.ProbeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	if len(req.Checks) == 0 || len(req.Checks) > edge.ProbeMaxChecks {
		writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.probeBad"))
		return
	}
	for _, c := range req.Checks {
		if !edge.ValidProbe(c) {
			writeError(w, http.StatusBadRequest, msgs.Tc(ctx, "edge.probeBadCheck", c.Type, c.Host, c.Port))
			return
		}
	}
	st, sess, ok := s.probeEdge(ctx, edgeIDParam(r))
	if !ok {
		writeError(w, http.StatusConflict, msgs.Tc(ctx, "edge.probeUnavailable"))
		return
	}
	res, err := runProbe(ctx, sess, req.Checks)
	s.db.Audit(ctx, auth.Username(ctx), "edge.probe", edgeName(st), auditOutcome(err), req.Checks)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"edge": edgeName(st), "results": res.Results})
}
