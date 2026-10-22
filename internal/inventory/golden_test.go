package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// Снимок fixtures-хоста целиком — эталон: перевод встроенных разборщиков
// на описания (svcdesc, engine:) и любые правки сканера не должны его
// менять. Меняющееся от запуска к запуску (время скана, длительности,
// mtime файлов, «осталось дней» у сертификатов) обнулено.
// Обновить эталон: NKT_UPDATE_GOLDEN=1 go test ./internal/inventory -run TestFixturesSnapshotGolden
func TestFixturesSnapshotGolden(t *testing.T) {
	s := fixtureScanner(t)
	s.cfg.DataDir = t.TempDir()
	snap, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snap.TS, snap.ScanMS, snap.Digest = "", 0, ""
	for i := range snap.Sources {
		snap.Sources[i].DurationMS = 0
	}
	for i := range snap.Files {
		snap.Files[i].ModTime = time.Time{}
	}
	snap.Malware.CheckedAt, snap.Malware.DurationMS = "", 0
	for i := range snap.Certs {
		snap.Certs[i].DaysLeft = 0
	}
	got, _ := json.MarshalIndent(snap, "", " ")
	// Сроки в текстах находок («через 12 дн.», «просрочен на 873 дн.»).
	got = regexp.MustCompile(`\d+ (дн\.|дней|дня|день|day\(s\)|days?)`).ReplaceAll(got, []byte("N $1"))
	// Метки времени, отсчитанные от «сейчас» (баны fail2ban и т. п.).
	got = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)`).ReplaceAll(got, []byte("TIME"))
	golden := filepath.Join("testdata", "fixtures_snapshot.json")
	if os.Getenv("NKT_UPDATE_GOLDEN") == "1" {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		g, w := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
		i := 0
		for i < len(g) && i < len(w) && bytes.Equal(g[i], w[i]) {
			i++
		}
		lo, hi := max(i-4, 0), min(i+4, len(g), len(w))
		t.Fatalf("снимок fixtures-хоста изменился (%s), строка %d:\nсейчас:\n%s\nэталон:\n%s", golden, i+1,
			bytes.Join(g[lo:hi], []byte("\n")), bytes.Join(w[lo:hi], []byte("\n")))
	}
}
