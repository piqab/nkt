package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestManifestVersions(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	id1, v1, err := db.SaveManifestVersion(ctx, Manifest{Name: "ingress-nginx", Content: "a: 1\n", Author: "admin"}, `[{"cluster":"c1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	id2, v2, err := db.SaveManifestVersion(ctx, Manifest{Name: "ingress-nginx", Content: "a: 2\n", Author: "admin", Note: "bump"}, `[]`)
	if err != nil || id2 != id1 || v2 <= v1 {
		t.Fatalf("%d %d %d %d %v", id1, id2, v1, v2, err)
	}
	m, err := db.ManifestByID(ctx, id1)
	if err != nil || m.Content != "a: 2\n" || m.Note != "bump" {
		t.Fatalf("%+v %v", m, err)
	}
	vs, _ := db.ManifestVersions(ctx, id1, 0)
	if len(vs) != 2 || vs[0].ID != v2 || vs[1].Results != `[{"cluster":"c1"}]` {
		t.Fatalf("%+v", vs)
	}
	v, err := db.ManifestVersion(ctx, v1)
	if err != nil || v.Content != "a: 1\n" {
		t.Fatalf("%+v %v", v, err)
	}
	if err := db.DeleteManifest(ctx, id1); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.ListManifests(ctx); len(list) != 0 {
		t.Errorf("остались: %+v", list)
	}
	if _, err := db.ManifestVersion(ctx, v1); err == nil {
		t.Error("версия пережила удаление")
	}
}
