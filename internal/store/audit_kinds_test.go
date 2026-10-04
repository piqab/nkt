package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Виды журнала берутся из самих записей: новый раздел (clamav, system)
// попадает в фильтр без правки интерфейса.
func TestAuditKinds(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for _, a := range []string{"clamav.scan", "clamav.install", "system.reboot", "firewall.rule.add", "login"} {
		db.Audit(ctx, "admin", a, "", "ok", nil)
	}
	kinds, err := db.AuditKinds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(kinds, ","); got != "clamav,firewall,login,system" {
		t.Fatalf("виды: %s", got)
	}
}
