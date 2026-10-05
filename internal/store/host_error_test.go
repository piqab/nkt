package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
)

// Причина ошибки хоста из каталога хранится ключом: читается на языке по
// умолчанию, переводится на язык запроса и переживает экспорт как есть.
func TestHostErrorLocalized(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	id, err := db.CreateHost(ctx, "web-1", "192.0.2.1", 22, "root", "password", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetHostStatus(ctx, id, HostStatusError, HostError(msgs.Errorf("hub.installInterruptedByRestart"))); err != nil {
		t.Fatal(err)
	}
	h, err := db.HostByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if h.ErrorMsg != msgs.T(msgs.DefaultLang, "hub.installInterruptedByRestart") {
		t.Errorf("ErrorMsg = %q", h.ErrorMsg)
	}
	if got := h.LocalizedError(msgs.EN); got != msgs.T(msgs.EN, "hub.installInterruptedByRestart") {
		t.Errorf("EN = %q", got)
	}
	if raw := h.RawError(); raw == h.ErrorMsg {
		t.Errorf("RawError lost the key: %q", raw)
	}

	// Обычная ошибка — текстом, без ключа.
	if err := db.SetHostStatus(ctx, id, HostStatusError, HostError(errors.New("ssh: handshake failed"))); err != nil {
		t.Fatal(err)
	}
	h, _ = db.HostByID(ctx, id)
	if h.ErrorKey != "" || h.LocalizedError(msgs.EN) != "ssh: handshake failed" {
		t.Errorf("plain error: key=%q text=%q", h.ErrorKey, h.LocalizedError(msgs.EN))
	}
}
