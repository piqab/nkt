package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/msgs"
)

// handleGenerateSelfSigned issues one self-signed certificate per name.
//
// Several names could go into a single certificate as SANs, and that used to
// be what happened — but it made "a.local, b.local" produce one certificate
// named after the first, which reads as the rest having been ignored. A
// separate certificate per name is what the comma is taken to mean here;
// pass one name to get one certificate.
//
// It never edits nginx or haproxy configuration: each result carries a
// snippet the caller pastes through the validated config editor, which
// already handles the validate-or-roll-back path.
func (s *Server) handleGenerateSelfSigned(w http.ResponseWriter, r *http.Request) {
	var req control.SelfSignedRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Names) == 0 {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "certgen.nameRequired"))
		return
	}

	user := auth.Username(r.Context())
	results := make([]control.SelfSignedResult, 0, len(req.Names))

	for _, name := range req.Names {
		one := req
		one.Names = []string{name}
		res, err := s.certs.GenerateSelfSigned(r.Context(), user, one)
		if err != nil {
			s.db.Audit(r.Context(), user, "cert.generate_selfsigned", name, "error", err.Error())
			// Report what was already created alongside the failure: some
			// certificates now exist on the host, and saying only "failed"
			// would leave the operator guessing which.
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": err.Error(), "results": results,
			})
			return
		}
		results = append(results, res)
	}

	// The new files do not appear in the certificate inventory until some
	// configuration references them, but a rescan costs nothing and keeps the
	// snapshot current for anything else that changed underneath it.
	s.rescanLater()
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

type renewRequest struct {
	Lineage     string `json:"lineage"`
	RestartPIDs []int  `json:"restart_pids,omitempty"`
}

// handleRenewCertbot starts a certbot-managed certificate lineage renewal in
// the background and returns a job ID immediately — the operation (stop
// services, run certbot, recombine any haproxy copy, restart services) can
// legitimately take minutes, and the caller polls handleRenewJobStatus for
// progress rather than waiting on one long request.
func (s *Server) handleRenewCertbot(w http.ResponseWriter, r *http.Request) {
	var req renewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Lineage == "" {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "certgen.lineageRequired"))
		return
	}

	user := auth.Username(r.Context())
	id, err := s.certs.StartRenewCertbot(user, req.Lineage, restartSet(req.RestartPIDs))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": id})
}

type issueRequest struct {
	Domains []string `json:"domains"`
	// RestartPIDs — ручные процессы на 80/443, которые оператор разрешил
	// остановить и поднять заново (см. control.StandalonePlan).
	RestartPIDs []int `json:"restart_pids,omitempty"`
}

func restartSet(pids []int) map[int]bool {
	out := map[int]bool{}
	for _, p := range pids {
		out[p] = true
	}
	return out
}

// handleIssueCertbot starts issuing a brand-new Let's Encrypt certificate
// for domain(s) certbot doesn't manage yet — unlike handleRenewCertbot,
// which only re-issues an existing lineage. Same background-job pattern:
// returns a job ID immediately, the caller polls handleRenewJobStatus.
func (s *Server) handleIssueCertbot(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user := auth.Username(r.Context())
	id, err := s.certs.StartIssueCertbot(user, req.Domains, restartSet(req.RestartPIDs))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": id})
}

// handleRenewJobStatus reports everything a renew job has logged so far, for
// the progress window to poll. A 404 means the ID never existed or was
// evicted a while after finishing — the caller should stop polling either way.
func (s *Server) handleRenewJobStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "job")
	events, done, errMsg, ok := s.certs.RenewJobStatus(id)
	if !ok {
		writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "job.notFound"))
		return
	}
	events = control.LocalizeRenewEvents(msgs.LangFromRequest(r), events)
	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"done":   done,
		"error":  errMsg,
	})
}

// handleCertLineages lists the certbot lineages found on the host, with
// their expiry, to populate the "собрать PEM для haproxy" form without the
// operator having to know or type the exact directory name.
func (s *Server) handleCertLineages(w http.ResponseWriter, r *http.Request) {
	lineages, err := s.certs.ListLetsEncryptLineages()
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lineages": lineages})
}

// handleHAProxyCertPaths lists the haproxy certificate paths the last scan
// actually found — the exact rows "Подробности" shows — so the combine form
// can offer to overwrite one of them in place.
func (s *Server) handleHAProxyCertPaths(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"paths": s.certs.ListHAProxyCertPaths()})
}

type combineRequest struct {
	Lineage    string `json:"lineage"`
	TargetPath string `json:"target_path"`
}

// handleCombineForHAProxy packages an already-issued certbot lineage into
// the single PEM haproxy's "crt" needs. Unlike renew, this never calls
// certbot: it only repackages a certificate that already exists. With
// TargetPath set to a path handleHAProxyCertPaths returned, it overwrites
// that file in place and reloads haproxy; left empty, it writes a new file
// and returns a directive to paste in by hand.
func (s *Server) handleCombineForHAProxy(w http.ResponseWriter, r *http.Request) {
	var req combineRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Lineage == "" {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "certgen.lineageRequired"))
		return
	}

	user := auth.Username(r.Context())
	res, err := s.certs.CombineForHAProxy(r.Context(), user, req.Lineage, req.TargetPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// The new/overwritten file does not appear in the certificate inventory
	// with its new expiry until the next scan, but a rescan costs nothing.
	s.rescanLater()
	writeJSON(w, http.StatusOK, res)
}

// handleStandalonePlan показывает, кто держит 80/443, — до подтверждения
// выпуска или продления, чтобы оператор видел, что будет остановлено.
func (s *Server) handleStandalonePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.certs.StandalonePlan(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handleCertTools отвечает, есть ли на хосте certbot и openssl: без
// certbot «выпустить» и «продлить» бессмысленны, и раздел говорит об этом
// при входе, а не отказом на нажатие.
func (s *Server) handleCertTools(w http.ResponseWriter, r *http.Request) {
	c := s.scanner.Collector()
	tools := map[string]any{}
	for _, name := range []string{"certbot", "openssl"} {
		present := collect.Which(r.Context(), c, name)
		version := ""
		if present {
			if res, err := c.Run(r.Context(), name, "--version"); err == nil {
				version = strings.TrimSpace(strings.SplitN(res.Output(), "\n", 2)[0])
			}
		}
		tools[name] = map[string]any{"present": present, "version": version}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

// handleCertSnippets отдаёт заготовки конфигураций nginx/haproxy/caddy
// под одну lineage — с путями этого хоста и именами из сертификата.
func (s *Server) handleCertSnippets(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	lineages, err := s.certs.ListLetsEncryptLineages()
	if err != nil {
		fail(w, r, err)
		return
	}
	var names []string
	found := false
	for _, l := range lineages {
		if l.Name == name {
			names, found = l.Names, true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "такой lineage нет в /etc/letsencrypt/live")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"snippets": control.CertSnippets(name, names, s.cfg.NginxMainConfig, s.cfg.HAProxyMainConf, s.cfg.CaddyMainConfig),
	})
}
