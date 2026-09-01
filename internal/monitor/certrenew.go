package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/inventory"
)

// certRenewActor is the audit-log actor for renewals the scheduler triggers
// on its own, without any admin clicking a button.
const certRenewActor = "system:cert-renew"

// CertRenewer runs certbot for every certbot-managed certificate the last
// scan found due for renewal — a fallback for hosts where certbot.timer or
// its cron job never got enabled, so an expired certificate would otherwise
// sit there until someone notices the finding and clicks "продлить" by hand.
type CertRenewer struct {
	cfg     *config.Config
	scanner *inventory.Scanner
	certs   *control.CertManager
}

// NewCertRenewer builds the auto-renew job. It is only started when
// config.AutoRenewCerts is on; RunOnce itself does not check the flag.
func NewCertRenewer(cfg *config.Config, scanner *inventory.Scanner, certs *control.CertManager) *CertRenewer {
	return &CertRenewer{cfg: cfg, scanner: scanner, certs: certs}
}

// RunOnce renews every certbot lineage that is expired or within
// AutoRenewCertsWithin of expiring, continuing past individual failures so
// one broken certificate cannot block the rest. It reports how many lineages
// it renewed and, if any failed, an error summarising which.
func (r *CertRenewer) RunOnce(ctx context.Context) (int, error) {
	snap := r.scanner.Latest()
	if snap == nil {
		return 0, nil
	}
	withinDays := int(r.cfg.AutoRenewCertsWithin.Hours() / 24)

	seen := map[string]bool{}
	renewed := 0
	var errs []string
	for _, cert := range snap.Certs {
		if cert.Renewal.Tool != "certbot" || !cert.Renewal.Managed || cert.Renewal.Lineage == "" {
			continue
		}
		if cert.DaysLeft > withinDays {
			continue
		}
		lineage := cert.Renewal.Lineage
		if seen[lineage] {
			continue // one lineage often backs several endpoints
		}
		seen[lineage] = true

		if _, err := r.certs.RenewCertbot(ctx, certRenewActor, lineage); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", lineage, err))
			continue
		}
		renewed++
	}
	if len(errs) > 0 {
		return renewed, fmt.Errorf("%d/%d продлений не удались: %s",
			len(errs), len(errs)+renewed, strings.Join(errs, "; "))
	}
	return renewed, nil
}
