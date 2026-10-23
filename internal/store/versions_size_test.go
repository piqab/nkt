package store

import (
	"context"
	"path/filepath"
	"testing"
)

// В списке редакций — размер текста в байтах (окно истории версий), а
// сам текст — только у одной редакции.
func TestVersionListsCarrySize(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	sid, err := db.CreateScript(ctx, Script{Name: "s", Content: "wait web-1 online 1m\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateScript(ctx, Script{ID: sid, Name: "s", Content: "# привет\n"}); err != nil {
		t.Fatal(err)
	}
	vs, err := db.ScriptVersions(ctx, sid, 10)
	if err != nil || len(vs) != 2 {
		t.Fatalf("script versions: %+v %v", vs, err)
	}
	// Новые сверху; кириллица — по два байта.
	if vs[0].Size != int64(len("# привет\n")) || vs[1].Size != int64(len("wait web-1 online 1m\n")) || vs[0].Content != "" {
		t.Errorf("script sizes: %+v", vs)
	}

	pid, err := db.CreateProfile(ctx, Profile{Name: "p", Content: "packages: [htop]\n"})
	if err != nil {
		t.Fatal(err)
	}
	if pv, _ := db.ProfileVersions(ctx, pid, 10); len(pv) != 1 || pv[0].Size != int64(len("packages: [htop]\n")) {
		t.Errorf("profile versions: %+v", pv)
	}

	did, err := db.CreateSvcDesc(ctx, SvcDesc{Name: "d", Content: "name: d\n"})
	if err != nil {
		t.Fatal(err)
	}
	if dv, _ := db.SvcDescVersions(ctx, did, 10); len(dv) != 1 || dv[0].Size != int64(len("name: d\n")) {
		t.Errorf("svcdesc versions: %+v", dv)
	}
}
