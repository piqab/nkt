package hub

import (
	"context"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Управление API-токенами — только администратору в браузере: токен
// управлять токенами не может (их маршрутов нет в tokenPolicy).

type tokenReq struct {
	Name   string   `json:"name"`
	Role   string   `json:"role"`
	Hosts  []int64  `json:"hosts"`
	Groups []string `json:"groups"`
	IPs    []string `json:"ips"`
	// ExpiresDays — срок в днях от сейчас; 0 — бессрочный. При правке
	// KeepExpiry оставляет прежний срок.
	ExpiresDays int  `json:"expires_days"`
	KeepExpiry  bool `json:"keep_expiry"`
	ViaEdge     bool `json:"via_edge"`
	// Perms — права и потолки роли «по правам».
	Perms store.TokenPerms `json:"perms"`
}

// validate — проверка и приведение полей; хосты и группы — только
// существующие.
// flyDomainRe — домен своих маршрутов Fly: имена DNS без звёздочки.
var flyDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func (s *Server) validateTokenReq(r *http.Request, req *tokenReq) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 64 || strings.ContainsAny(req.Name, "\n\r\t") {
		return msgs.Errorf("hub.tokenBadName")
	}
	if req.Role != store.TokenRoleRead && req.Role != store.TokenRoleAdmin && req.Role != store.TokenRoleCustom {
		return msgs.Errorf("hub.tokenBadRole", req.Role)
	}
	if req.Role == store.TokenRoleCustom {
		if err := s.validateTokenPerms(r.Context(), &req.Perms); err != nil {
			return err
		}
	} else {
		req.Perms = store.TokenPerms{}
	}
	if req.ExpiresDays < 0 || req.ExpiresDays > 3650 {
		return msgs.Errorf("hub.tokenBadExpiry", req.ExpiresDays)
	}
	ctx := r.Context()
	groups := s.hostGroups(ctx)
	hosts := []int64{}
	for _, id := range req.Hosts {
		if _, ok := groups[id]; !ok {
			return msgs.Errorf("hub.tokenBadHost", id)
		}
		if !slices.Contains(hosts, id) {
			hosts = append(hosts, id)
		}
	}
	req.Hosts = hosts
	known, err := s.db.ListHostGroups(ctx)
	if err != nil {
		return err
	}
	if g := s.hub.LocalHostGroup(ctx); g != "" && !slices.Contains(known, g) {
		known = append(known, g)
	}
	gs := []string{}
	for _, g := range req.Groups {
		g = strings.TrimSpace(g)
		if !slices.Contains(known, g) {
			return msgs.Errorf("hub.tokenBadGroup", g)
		}
		if !slices.Contains(gs, g) {
			gs = append(gs, g)
		}
	}
	req.Groups = gs
	ips := []string{}
	for _, raw := range req.IPs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if p, err := netip.ParsePrefix(raw); err == nil {
			raw = p.Masked().String()
		} else if a, err := netip.ParseAddr(raw); err == nil {
			raw = a.Unmap().String()
		} else {
			return msgs.Errorf("hub.tokenBadIP", raw)
		}
		if !slices.Contains(ips, raw) {
			ips = append(ips, raw)
		}
	}
	if len(ips) > 64 {
		return msgs.Errorf("hub.tokenBadIP", "…")
	}
	req.IPs = ips
	return nil
}

func expiryFromDays(days int) string {
	if days == 0 {
		return ""
	}
	return store.FormatTime(time.Now().AddDate(0, 0, days))
}

func tokenIDParam(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}

// tokenAudit — пределы токена для журнала действий (без секрета).
func tokenAudit(t store.APIToken) map[string]any {
	out := map[string]any{"role": t.Role, "hosts": t.Hosts, "groups": t.Groups, "ips": t.IPs, "expires_at": t.ExpiresAt, "via_edge": t.ViaEdge}
	if t.Role == store.TokenRoleCustom {
		out["perms"] = t.Perms
	}
	return out
}

// handleTokens — GET /hub/tokens.
func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListAPITokens(r.Context())
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

// handleTokenCreate — POST /hub/tokens: секрет показывается один раз.
func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.validateTokenReq(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	keyID, secret, err := newTokenSecret()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	enc, err := secretbox.Encrypt(s.hub.key, []byte(secret))
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	ctx := r.Context()
	user := auth.Username(ctx)
	t := store.APIToken{Name: req.Name, KeyID: keyID, SecretEnc: enc, Role: req.Role, Hosts: req.Hosts, Groups: req.Groups,
		IPs: req.IPs, ExpiresAt: expiryFromDays(req.ExpiresDays), ViaEdge: req.ViaEdge, Author: user, Perms: req.Perms}
	id, err := s.db.CreateAPIToken(ctx, t)
	s.db.Audit(ctx, user, "token.create", req.Name, auditOutcome(err), tokenAudit(t))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.tokenNameTaken", req.Name))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "key_id": keyID, "secret": secret, "token": bearerToken(keyID, secret)})
}

// handleTokenUpdate — PUT /hub/tokens/{id}: имя, роль, пределы, адреса,
// срок. Секрет не меняется.
func (s *Server) handleTokenUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := tokenIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	t, err := s.db.APITokenByID(ctx, id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.tokenMissing", id))
		return
	}
	var req tokenReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.validateTokenReq(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	t.Name, t.Role, t.Hosts, t.Groups, t.IPs, t.ViaEdge, t.Perms = req.Name, req.Role, req.Hosts, req.Groups, req.IPs, req.ViaEdge, req.Perms
	if !req.KeepExpiry {
		t.ExpiresAt = expiryFromDays(req.ExpiresDays)
	}
	err = s.db.UpdateAPIToken(ctx, t)
	s.db.Audit(ctx, auth.Username(ctx), "token.update", t.Name, auditOutcome(err), tokenAudit(t))
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.tokenNameTaken", req.Name))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTokenRotate — POST /hub/tokens/{id}/rotate: новый ключ и секрет,
// старые перестают действовать сразу.
func (s *Server) handleTokenRotate(w http.ResponseWriter, r *http.Request) {
	id, err := tokenIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	t, err := s.db.APITokenByID(ctx, id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.tokenMissing", id))
		return
	}
	keyID, secret, err := newTokenSecret()
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	enc, err := secretbox.Encrypt(s.hub.key, []byte(secret))
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	err = s.db.SetAPITokenSecret(ctx, id, keyID, enc)
	s.db.Audit(ctx, auth.Username(ctx), "token.rotate", t.Name, auditOutcome(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "key_id": keyID, "secret": secret, "token": bearerToken(keyID, secret)})
}

// handleTokenDelete — DELETE /hub/tokens/{id}: отзыв.
func (s *Server) handleTokenDelete(w http.ResponseWriter, r *http.Request) {
	id, err := tokenIDParam(r)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	t, err := s.db.APITokenByID(ctx, id)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.tokenMissing", id))
		return
	}
	err = s.db.DeleteAPIToken(ctx, id)
	s.db.Audit(ctx, auth.Username(ctx), "token.delete", t.Name, auditOutcome(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- задания хаба по номеру -------------------------------------------------

// hubJobFromReq — задание хаба; токену с пределами — только запущенное им
// самим.
func (s *Server) hubJobFromReq(w http.ResponseWriter, r *http.Request) (store.Job, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return store.Job{}, false
	}
	ctx := r.Context()
	j, err := s.db.JobByID(ctx, id)
	if t, ok := tokenFromContext(ctx); err == nil && ok && t.Scoped() && j.Author != auth.Username(ctx) {
		err = store.ErrNotFound
	}
	if err != nil {
		writeErr(w, r, http.StatusNotFound, msgs.Errorf("hub.jobMissing", id))
		return j, false
	}
	j.Resumable = s.jobs != nil && s.jobs.IsResumable(j.Kind)
	lang := msgs.FromContext(ctx)
	j.Title = msgs.Render(lang, j.TitleKey, j.TitleArgs, j.Title)
	j.StepName = msgs.Render(lang, j.StepKey, j.StepArgs, j.StepName)
	j.Error = msgs.Render(lang, j.ErrorKey, j.ErrorArgs, j.Error)
	return j, true
}

// handleHubJob — GET /hub/jobs/{id}.
func (s *Server) handleHubJob(w http.ResponseWriter, r *http.Request) {
	if j, ok := s.hubJobFromReq(w, r); ok {
		writeJSON(w, http.StatusOK, j)
	}
}

// handleHubJobLog — GET /hub/jobs/{id}/log?after=N.
func (s *Server) handleHubJobLog(w http.ResponseWriter, r *http.Request) {
	j, ok := s.hubJobFromReq(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	lines, err := s.db.JobLog(r.Context(), j.ID, after, 2000)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	lang := msgs.FromContext(r.Context())
	for i := range lines {
		lines[i].Text = msgs.Render(lang, lines[i].Key, lines[i].Args, lines[i].Text)
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": j, "lines": lines})
}

// validateTokenPerms — права роли «по правам»: известные действия, только
// существующие конвейеры, сценарии и кластеры, имена namespace, разумные
// потолки. Потолки суммой у compose без потолка сервиса не посчитать.
func (s *Server) validateTokenPerms(ctx context.Context, p *store.TokenPerms) error {
	bad := func(what string) error { return msgs.Errorf("hub.tokenBadPerms", what) }
	acts := []string{}
	for _, a := range p.Actions {
		if !slices.Contains(store.TokenActions, a) && !s.pluginActionKnown(ctx, a) {
			return bad(a)
		}
		if !slices.Contains(acts, a) {
			acts = append(acts, a)
		}
	}
	if len(acts) == 0 {
		return bad("actions")
	}
	p.Actions = acts
	names := func(list []string, known []string, what string) ([]string, error) {
		out := []string{}
		for _, n := range list {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			if !slices.Contains(known, n) {
				return nil, bad(what + " " + n)
			}
			if !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
		return out, nil
	}
	var known []string
	if list, err := s.db.ListPipelines(ctx); err == nil {
		for _, x := range list {
			known = append(known, x.Name)
		}
	}
	var err error
	if p.Pipelines, err = names(p.Pipelines, known, "pipeline"); err != nil {
		return err
	}
	known = nil
	if list, err := s.db.ListScripts(ctx); err == nil {
		for _, x := range list {
			known = append(known, x.Name)
		}
	}
	if p.Scripts, err = names(p.Scripts, known, "script"); err != nil {
		return err
	}
	known = nil
	if list, err := s.db.ListClusters(ctx); err == nil {
		for _, x := range list {
			known = append(known, x.Name)
		}
	}
	if p.Clusters, err = names(p.Clusters, known, "cluster"); err != nil {
		return err
	}
	nss := []string{}
	for _, n := range p.Namespaces {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !NamespaceRe.MatchString(n) || strings.HasPrefix(n, "kube-") || n == "default" {
			return bad("namespace " + n)
		}
		if !slices.Contains(nss, n) {
			nss = append(nss, n)
		}
	}
	if len(nss) > 32 {
		return bad("namespaces")
	}
	p.Namespaces = nss
	if slices.Contains(acts, store.TokenActK8sApply) && len(nss) == 0 && !slices.Contains(acts, store.TokenActK8sNamespace) {
		return bad("namespaces")
	}
	p.FlyDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(p.FlyDomain), "."))
	if p.FlyDomain != "" && (!flyDomainRe.MatchString(p.FlyDomain) || !strings.Contains(p.FlyDomain, ".")) {
		return bad("fly_domain")
	}
	if slices.Contains(acts, store.TokenActFlyOwn) && p.FlyDomain == "" {
		return bad("fly_domain")
	}
	if p.FlyGateway < 0 {
		return bad("fly_gateway")
	}
	l := p.Limits
	if l.CPU < 0 || l.CPU > 4096 || l.HostCPU < 0 || l.HostCPU > 4096 || l.ServiceCPU < 0 || l.ServiceCPU > 1024 || l.K8sCPU < 0 || l.K8sCPU > 4096 ||
		l.MemMB < 0 || l.HostMemMB < 0 || l.ServiceMemMB < 0 || l.K8sMemMB < 0 || l.DiskGB < 0 || l.K8sStorageGB < 0 || l.MinFreeGB < 0 ||
		l.VMs < 0 || l.VMs > 1000 || l.VMCPU < 0 || l.VMCPU > 512 || l.VMMemMB < 0 || l.VMDiskGB < 0 || l.VMSnapshots < 0 || l.VMSnapshots > 100 || l.Ports < 0 || l.Ports > 1000 || l.FlyRoutes < 0 || l.FlyRoutes > 1000 || l.K8sNamespaces < 0 || l.K8sNamespaces > 1000 || l.K8sClusters < 0 || l.K8sClusters > 100 || l.K8sPods < 0 || l.DeploysPerHour < 0 || l.DeploysPerHour > 10000 {
		return bad("limits")
	}
	if slices.Contains(acts, store.TokenActPipelineDeploy) && (l.CPU > 0 || l.HostCPU > 0) && l.ServiceCPU == 0 {
		return bad("service_cpu")
	}
	if slices.Contains(acts, store.TokenActPipelineDeploy) && (l.MemMB > 0 || l.HostMemMB > 0) && l.ServiceMemMB == 0 {
		return bad("service_mem_mb")
	}
	return nil
}
