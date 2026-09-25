package guestcred

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, dir)
	ctx := context.Background()
	if info, err := s.Info(ctx, "lxd", "c1"); err != nil || info.Set {
		t.Fatalf("пусто: %+v %v", info, err)
	}
	if err := s.Put(ctx, "lxd", "c1", "admin", "s3cret:pass'x", "alice"); err != nil {
		t.Fatal(err)
	}
	info, _ := s.Info(ctx, "lxd", "c1")
	if !info.Set || info.User != "admin" || info.SetBy != "alice" {
		t.Fatalf("%+v", info)
	}
	raw, _, _ := db.KVGet(ctx, kvKey("lxd", "c1"))
	if strings.Contains(raw, "s3cret") {
		t.Fatal("пароль лежит открыто")
	}
	if p, err := s.Secret(ctx, Ref("lxd", "c1")); err != nil || p != "s3cret:pass'x" {
		t.Fatalf("%q %v", p, err)
	}
	// Новый Store с тем же каталогом — тот же ключ.
	if _, p, err := New(db, dir).Reveal(ctx, "lxd", "c1"); err != nil || p != "s3cret:pass'x" {
		t.Fatalf("после перезапуска: %q %v", p, err)
	}
	if err := s.Put(ctx, "vm", "a b", "admin", "12345678", "x"); err == nil {
		t.Error("принято плохое имя")
	}
	if err := s.Put(ctx, "vm", "w", "admin", "short", "x"); err == nil {
		t.Error("принят короткий пароль")
	}
	if g := Generate(); len(g) != 16 {
		t.Errorf("generate: %q", g)
	}
	if ShellQuote("a'b") != `'a'\''b'` {
		t.Error("quote")
	}
}
