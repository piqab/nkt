package hub

import (
	"net/http"
	"net/netip"
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
}

// validate — проверка и приведение полей; хосты и группы — только
// существующие.
func (s *Server) validateTokenReq(r *http.Request, req *tokenReq) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 64 || strings.ContainsAny(req.Name, "\n\r\t") {
		return msgs.Errorf("hub.tokenBadName")
	}
	if req.Role != store.TokenRoleRead && req.Role != store.TokenRoleAdmin {
		return msgs.Errorf("hub.tokenBadRole", req.Role)
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
	return map[string]any{"role": t.Role, "hosts": t.Hosts, "groups": t.Groups, "ips": t.IPs, "expires_at": t.ExpiresAt, "via_edge": t.ViaEdge}
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
		IPs: req.IPs, ExpiresAt: expiryFromDays(req.ExpiresDays), ViaEdge: req.ViaEdge, Author: user}
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
	t.Name, t.Role, t.Hosts, t.Groups, t.IPs, t.ViaEdge = req.Name, req.Role, req.Hosts, req.Groups, req.IPs, req.ViaEdge
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
