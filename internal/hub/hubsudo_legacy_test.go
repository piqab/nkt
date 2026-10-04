package hub

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/piqab/nkt/internal/hubsudo"
	"github.com/piqab/nkt/internal/store"
)

// Переезд: импорт полного экспорта запоминает ключ подписи прежнего хаба;
// хост с прежним ключом узнаётся как «наш, прежний»; следующий полный
// экспорт переносит ключ дальше; без мастер-ключа в файле — ничего.
func TestLegacySignKeysOnImport(t *testing.T) {
	ctx := context.Background()
	oldHub, _ := newTestManager(t)
	oldHub.key = []byte("old-master-key-0123456789abcdef!")
	oldPub := hubsudo.PublicText(oldHub.signKey())

	m, _ := newTestManager(t)
	if _, legacy, ok := m.matchKey(oldPub); ok || legacy {
		t.Fatal("foreign key recognised before import")
	}
	rep := m.ImportHosts(ctx, store.HubExport{Version: store.ExportFormatVersion,
		MasterKey: base64.StdEncoding.EncodeToString(oldHub.key)}, nil)
	if len(rep.Errors) > 0 {
		t.Fatalf("import: %v", rep.Errors)
	}
	key, legacy, ok := m.matchKey(oldPub)
	if !ok || !legacy || string(key.Seed()) != string(oldHub.signKey().Seed()) {
		t.Fatalf("old key: ok=%v legacy=%v", ok, legacy)
	}
	if _, legacy, ok := m.matchKey(hubsudo.PublicText(m.signKey())); !ok || legacy {
		t.Fatal("own key")
	}
	// Повторный импорт — без дублей.
	_ = m.ImportHosts(ctx, store.HubExport{Version: store.ExportFormatVersion, MasterKey: base64.StdEncoding.EncodeToString(oldHub.key)}, nil)
	if n := len(m.LegacySignSeeds()); n != 1 {
		t.Fatalf("seeds: %d", n)
	}
	// Следующий хаб получает и ключ этого, и унаследованный.
	exp, err := m.ExportHub(ctx, true, false)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := newTestManager(t)
	next.key = []byte("next-master-key-0123456789abcde!")
	_ = next.ImportHosts(ctx, exp, nil)
	if _, legacy, ok := next.matchKey(oldPub); !ok || !legacy {
		t.Fatal("inherited key lost on the second move")
	}
	if _, legacy, ok := next.matchKey(hubsudo.PublicText(m.signKey())); !ok || !legacy {
		t.Fatal("previous hub key not kept")
	}
	// Экспорт без ключа ключей не несёт.
	exp, _ = m.ExportHub(ctx, false, false)
	if exp.MasterKey != "" || len(exp.LegacySignKeys) > 0 {
		t.Fatal("keys leaked into an export without key")
	}
}
