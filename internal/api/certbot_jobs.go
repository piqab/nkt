package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Выпуск и продление сертификата certbot — настоящим заданием хоста (с
// ?job=1): раньше это была фоновая горутина со своим списком в памяти —
// не видна в «Заданиях» и в индикаторе фоновых операций и терялась при
// перезапуске nkt. Без ?job=1 — прежний путь для старого интерфейса.

// KindCertbot — вид задания.
const KindCertbot = "certbot"

// CertbotParams — вход задания.
type CertbotParams struct {
	Op          string   `json:"op"` // renew | issue
	Lineage     string   `json:"lineage,omitempty"`
	Domains     []string `json:"domains,omitempty"`
	RestartPIDs []int    `json:"restart_pids,omitempty"`
	Force       bool     `json:"force,omitempty"`
}

func (s *Server) startCertbotJob(w http.ResponseWriter, r *http.Request, p CertbotParams) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	title, arg := "certbot.renewJob", p.Lineage
	if p.Op == "issue" {
		title, arg = "certbot.issueJob", strings.Join(p.Domains, ", ")
	}
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindCertbot, TitleKey: title, TitleArgs: []any{arg},
		// Одна очередь: два certbot разом спорят за порт 80 и за
		// остановленные ради него службы.
		Queue: "certbot", Author: auth.Username(r.Context()), Steps: 1, Params: p,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// certbotRunner выполняет KindCertbot.
type certbotRunner struct{ s *Server }

// Resumable — нет: certbot с середины не продолжить; после перезапуска
// задание честно помечается прерванным.
func (c *certbotRunner) Resumable() bool { return false }

func (c *certbotRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p CertbotParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	if c.s.certs == nil {
		return msgs.Errorf("hostop.unknown", "certbot")
	}
	msg := func(key string, args ...any) { jc.Log(key, args...) }
	raw := func(text string) { jc.Logf("%s", text) }
	switch p.Op {
	case "renew":
		if err := control.ValidateRenewCertbot(p.Lineage); err != nil {
			return err
		}
		jc.StepKey(1, 1, "certbot.renewStep", p.Lineage)
		return c.s.certs.RenewCertbotJob(ctx, jc.Job.Author, p.Lineage, restartSet(p.RestartPIDs), msg, raw)
	case "issue":
		domains, err := control.ValidateIssueCertbot(p.Domains)
		if err != nil {
			return err
		}
		jc.StepKey(1, 1, "certbot.issueStep", strings.Join(domains, ", "))
		return c.s.certs.IssueCertbotJob(ctx, jc.Job.Author, domains, restartSet(p.RestartPIDs), p.Force, msg, raw)
	}
	return msgs.Errorf("hostop.unknown", p.Op)
}
