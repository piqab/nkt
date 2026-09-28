package control

import (
	"bytes"
	"context"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/store"
)

// С 80% заполнения — находка высокой серьёзности; вытеснение освобождает
// место, начиная со старых версий истории файлов.
func TestFileHistoryFullAndPrune(t *testing.T) {
	m := configsSetupWithCollector(t, t.TempDir(), collect.NewFixtures(t.TempDir()))
	ctx := context.Background()
	if err := m.SetFileHistoryLimits(ctx, FileHistoryLimits{PerUploadMB: 1, TotalMB: 1, Days: 30}); err != nil {
		t.Fatal(err)
	}
	if len(m.FileHistoryFindings(ctx)) != 0 {
		t.Fatal("пустая история — без находки")
	}
	chunk := func(b byte) *bytes.Reader { return bytes.NewReader(bytes.Repeat([]byte{b}, 450<<10)) }
	for i, b := range []byte{'a', 'b'} {
		if _, _, err := m.RecordFileSnapshot(ctx, "/srv/site/big.bin", "files", "t", store.ActionUpload, "", chunk(b)); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	f := m.FileHistoryFindings(ctx)
	if len(f) != 1 || f[0].Severity != "high" {
		t.Fatalf("находка: %+v (usage %+v)", f, m.FileHistoryUsage(ctx))
	}
	if _, _, err := m.RecordFileSnapshot(ctx, "/srv/site/big.bin", "files", "t", store.ActionUpload, "", chunk('c')); err != nil {
		t.Fatal(err)
	}
	if removed := m.PruneFileHistory(ctx); removed == 0 {
		t.Fatal("ничего не вытеснено")
	}
	if u := m.FileHistoryUsage(ctx); u.Used > u.Limit {
		t.Errorf("после вытеснения %d > %d", u.Used, u.Limit)
	}
	list, _ := m.db.ListVersions(ctx, "/srv/site/big.bin", 10)
	if len(list) == 0 || list[0].Note != "" || list[len(list)-1].ID == 1 {
		t.Errorf("остались не самые новые: %+v", list)
	}
	if err := m.SetFileHistoryLimits(ctx, FileHistoryLimits{TotalMB: 0, Days: 1}); err == nil {
		t.Error("нулевой лимит принят")
	}
}
