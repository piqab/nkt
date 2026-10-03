package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Каждое состояние sudo, включая «narrow», записывается и читается через
// настоящую базу (у старого столбца sudo_status CHECK без «narrow»).
func TestHostSudoStatusRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := db.CreateHost(ctx, "h1", "10.0.0.1", 22, "deploy", HostAuthPassword, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{SudoStatusNarrow, SudoStatusNopasswd, SudoStatusPasswordRequired, SudoStatusRoot, SudoStatusUnknown, SudoStatusNarrow} {
		if err := db.SetHostSudoStatus(ctx, id, s); err != nil {
			t.Fatalf("set %q: %v", s, err)
		}
		h, err := db.HostByID(ctx, id)
		if err != nil || h.SudoStatus != s {
			t.Fatalf("after set %q: got %q, %v", s, h.SudoStatus, err)
		}
		list, _ := db.ListHosts(ctx)
		if len(list) != 1 || list[0].SudoStatus != s {
			t.Fatalf("list after set %q: %+v", s, list)
		}
	}
	if err := db.SetHostSudoStatus(ctx, id, "bogus"); err == nil {
		t.Fatal("bogus status accepted")
	}
	// Правка подключения сбрасывает и узкий.
	if err := db.UpdateHost(ctx, id, "h1", "10.0.0.1", 22, "other", HostAuthPassword); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.HostByID(ctx, id); h.SudoStatus != SudoStatusUnknown {
		t.Fatalf("after edit: %q", h.SudoStatus)
	}
	// Строка до миграции: только старый столбец — читается он.
	if _, err := db.ExecContext(ctx, `UPDATE hosts SET sudo_status = 'nopasswd', sudo_mode = '' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.HostByID(ctx, id); h.SudoStatus != SudoStatusNopasswd {
		t.Fatalf("legacy row: %q", h.SudoStatus)
	}
	// Экспорт и импорт переносят «narrow».
	if err := db.SetHostSudoStatus(ctx, id, SudoStatusNarrow); err != nil {
		t.Fatal(err)
	}
	h, _ := db.HostByID(ctx, id)
	exp := HostExport{Name: "h2", Addr: "10.0.0.2", SSHPort: 22, SSHUser: "deploy", SSHAuthKind: HostAuthPassword,
		SecretEnc: []byte("s"), Status: HostStatusNew, SudoStatus: h.SudoStatus, CreatedAt: Now()}
	id2, err := db.importOneHost(ctx, exp)
	if err != nil {
		t.Fatal(err)
	}
	if h2, _ := db.HostByID(ctx, id2); h2.SudoStatus != SudoStatusNarrow {
		t.Fatalf("imported: %q", h2.SudoStatus)
	}
	exp.SudoStatus = "from-the-future"
	if err := db.replaceHost(ctx, id2, exp); err != nil {
		t.Fatal(err)
	}
	if h2, _ := db.HostByID(ctx, id2); h2.SudoStatus != SudoStatusUnknown {
		t.Fatalf("unknown imported status: %q", h2.SudoStatus)
	}
	db.Close()
	// Повторное открытие (миграции на существующей базе) ничего не теряет.
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if h, _ := db.HostByID(ctx, id); h.SudoStatus != SudoStatusNarrow {
		t.Fatalf("after reopen: %q", h.SudoStatus)
	}
}

// База прежней версии (без sudo_mode) получает столбец при открытии, а
// прежнее состояние читается из sudo_status.
func TestSudoModeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, _ := db.CreateHost(ctx, "h1", "10.0.0.1", 22, "deploy", HostAuthPassword, []byte("secret"))
	if err := db.SetHostSudoStatus(ctx, id, SudoStatusNopasswd); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE hosts DROP COLUMN sudo_mode`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if h, _ := db.HostByID(ctx, id); h.SudoStatus != SudoStatusNopasswd {
		t.Fatalf("legacy status: %q", h.SudoStatus)
	}
	if err := db.SetHostSudoStatus(ctx, id, SudoStatusNarrow); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.HostByID(ctx, id); h.SudoStatus != SudoStatusNarrow {
		t.Fatalf("narrow after migration: %q", h.SudoStatus)
	}
}
