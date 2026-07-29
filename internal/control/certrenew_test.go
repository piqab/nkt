package control

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/store"
)

// renewSetup wires a CertManager against the repo's real fixtures tree,
// which carries a canned "certbot renew --cert-name app.example.com"
// response — unlike certgenSetup's throwaway temp directory, this lets a test
// exercise the actual command path instead of only validation.
func renewSetup(t *testing.T) (*CertManager, *store.DB) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatalf("resolve fixtures root: %v", err)
	}
	c := collect.NewFixtures(root)
	dbPath := filepath.Join(t.TempDir(), "nkt.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("открыть базу: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewCertManager(&config.Config{}, c, db), db
}

func TestRenewCertbotRejectsBadLineage(t *testing.T) {
	m, _ := renewSetup(t)
	for _, lineage := range []string{"", "../../etc/passwd", "site;rm -rf /", "site with spaces"} {
		if _, err := m.RenewCertbot(context.Background(), "test", lineage); err == nil {
			t.Errorf("lineage %q: ожидалась ошибка валидации", lineage)
		}
	}
}

func TestRenewCertbotSuccess(t *testing.T) {
	m, db := renewSetup(t)
	res, err := m.RenewCertbot(context.Background(), "test", "app.example.com")
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !res.OK() {
		t.Fatalf("ожидался успешный exit code, получено %d: %s", res.ExitCode, res.Output())
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "cert.renew", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("записей в журнале: %d, ожидалась 1", len(entries))
	}
	if entries[0].Result != "ok" || entries[0].Target != "app.example.com" || entries[0].Username != "test" {
		t.Errorf("неожиданная запись журнала: %+v", entries[0])
	}
}

func TestRenewCertbotFailureIsAudited(t *testing.T) {
	m, db := renewSetup(t)
	// No canned command for this lineage: Fixtures.Run returns exit 127.
	_, err := m.RenewCertbot(context.Background(), "test", "unknown-lineage.example.com")
	if err == nil {
		t.Fatal("ожидалась ошибка для lineage без заготовленного вывода")
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "cert.renew", Limit: 10})
	if err != nil {
		t.Fatalf("чтение журнала: %v", err)
	}
	if len(entries) != 1 || entries[0].Result != "error" {
		t.Fatalf("ожидалась одна запись с результатом error, получено: %+v", entries)
	}
}
