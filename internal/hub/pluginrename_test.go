package hub

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// Renaming a host carries the plugins installed on it over to the new
// name: the conf keeps the host by name, and without the cascade every
// plugin call would fail with plugin.hostGone until the conf is edited
// by hand. Plugins on other hosts and on the hub machine are untouched.
func TestHostRenameCascadesPluginHost(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Server{db: db, log: slog.New(slog.DiscardHandler)}
	ctx := context.Background()

	if err := s.savePlugins(ctx, []pluginConf{
		{Name: "dns", Host: "web-1", Port: 5380},
		{Name: "other", Host: "hv2", Port: 9100},
		{Name: "native", Host: localHostName, Port: 8787},
	}); err != nil {
		t.Fatal(err)
	}

	moved := s.cascadeHostRename(ctx, "web-1", "web-one", "admin")
	if len(moved) != 1 || moved[0] != "dns" {
		t.Fatalf("moved = %v, want [dns]", moved)
	}
	hosts := map[string]string{}
	for _, p := range s.plugins(ctx) {
		hosts[p.Name] = p.Host
	}
	if hosts["dns"] != "web-one" {
		t.Fatalf("dns host = %q, want web-one", hosts["dns"])
	}
	if hosts["other"] != "hv2" || hosts["native"] != localHostName {
		t.Fatalf("uninvolved confs changed: %v", hosts)
	}

	// A rename to the same name, a rename of an absent host and a rename
	// of the hub machine's constant match nothing and change nothing.
	for _, c := range [][2]string{{"web-one", "web-one"}, {"ghost", "x"}, {localHostName, "hub-box"}} {
		if got := s.cascadeHostRename(ctx, c[0], c[1], "admin"); got != nil {
			t.Fatalf("cascade(%q, %q) = %v, want nil", c[0], c[1], got)
		}
	}
	for _, p := range s.plugins(ctx) {
		if want := hosts[p.Name]; p.Host != want {
			t.Fatalf("no-op cascade changed %s: %q != %q", p.Name, p.Host, want)
		}
	}

	entries, _ := db.ListAudit(ctx, store.AuditFilter{Limit: 50})
	var found bool
	for _, e := range entries {
		if e.Action == "plugin.host_renamed" && e.Target == "web-one" && e.Username == "admin" {
			found = strings.Contains(e.Detail, "web-1") && strings.Contains(e.Detail, "dns")
		}
	}
	if !found {
		t.Fatalf("no plugin.host_renamed audit entry: %+v", entries)
	}
}
