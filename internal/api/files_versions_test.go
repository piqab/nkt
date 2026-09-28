package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/files"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/store"
)

// Правка в «Файлах» пишет историю (исходное + правка), дифф версии с
// текущим файлом и откат работают; версия чужого пути не отдаётся.
func TestFilesVersions(t *testing.T) {
	root := t.TempDir()
	run := func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		if argv[0] == "install" {
			data, _ := os.ReadFile(argv[len(argv)-2])
			_ = os.WriteFile(argv[len(argv)-1], data, 0o644)
		}
		return collect.CommandResult{}, nil
	}
	local := collect.NewLocal("", "", 0)
	fm := files.NewManager([]string{root}, local, run, nil, filepath.Join(root, ".nkt-tmp"))
	cfg := &config.Config{Mode: config.ModeLocal, DataDir: t.TempDir(), NginxRoot: "/etc/nginx", HAProxyRoot: "/etc/haproxy", CommandTimeout: 5 * time.Second}
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanner := inventory.New(cfg, local, db)
	s := &Server{cfg: cfg, db: db, files: fm, configs: control.NewConfigManager(cfg, local, db, scanner, control.NewServiceManager(cfg, local, db))}
	r := chi.NewRouter()
	r.Post("/files/write", s.handleFilesWrite)
	r.Get("/files/versions", s.handleFilesVersions)
	r.Get("/files/versions/{id}/diff", s.handleFilesVersionDiff)
	r.Post("/files/versions/{id}/rollback", s.handleFilesVersionRollback)
	call := func(method, url string, body any) (int, map[string]any) {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, url, &buf))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	path := filepath.Join(root, "site.conf")
	if err := os.WriteFile(path, []byte("a=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	txt, _ := fm.Read(path)
	if code, out := call("POST", "/files/write", map[string]any{"path": path, "content": "a=2\n", "expected_sha256": txt.SHA256, "note": "правка"}); code != 200 {
		t.Fatalf("write: %d %v", code, out)
	}
	code, out := call("GET", "/files/versions?path="+path, nil)
	list, _ := out["versions"].([]any)
	if code != 200 || len(list) != 2 {
		t.Fatalf("versions: %d %v", code, out)
	}
	latest := list[0].(map[string]any)
	first := list[1].(map[string]any)
	if latest["note"] != "правка" || latest["action"] != store.ActionEdit || first["action"] != store.ActionObserved {
		t.Errorf("версии: %v", list)
	}
	firstID := strconv.Itoa(int(first["id"].(float64)))
	if _, out := call("GET", "/files/versions/"+firstID+"/diff", nil); !strings.Contains(out["diff"].(string), "-a=1") || !strings.Contains(out["diff"].(string), "+a=2") {
		t.Errorf("diff: %v", out)
	}
	if code, out := call("POST", "/files/versions/"+firstID+"/rollback", nil); code != 200 {
		t.Fatalf("rollback: %d %v", code, out)
	}
	if b, _ := os.ReadFile(path); string(b) != "a=1\n" {
		t.Errorf("после отката: %q", b)
	}
	if _, out := call("GET", "/files/versions?path="+path, nil); len(out["versions"].([]any)) != 3 {
		t.Errorf("после отката версий: %v", out)
	}
	if code, _ := call("GET", "/files/versions?path=/etc/passwd", nil); code == 200 {
		t.Error("история пути вне корней")
	}
}
