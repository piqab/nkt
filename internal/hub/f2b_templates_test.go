package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// Шаблон на хосты: проверка обязательна (без неё и повторно по той же
// проверке — отказ), применяется ровно проверенное.
func TestF2BTemplateCheckThenApply(t *testing.T) {
	srv, db, root := localFixtureHub(t)
	srv.jobs.Register(KindF2BTemplate, NewF2BTemplateRunner(srv))
	ctx := auth.WithUser(context.Background(), store.User{Username: "admin", Role: store.RoleAdmin})
	call := func(h http.HandlerFunc, body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)).WithContext(ctx)
		w := httptest.NewRecorder()
		h(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	// Без проверки — отказ.
	if code, _ := call(srv.handleF2BTemplateApply, `{"token":"nope"}`); code != http.StatusConflict {
		t.Fatalf("apply without check: %d", code)
	}
	code, out := call(srv.handleF2BTemplateCheck, `{"name":"recidive","builtin":true,"host_ids":[-1, 999]}`)
	if code != http.StatusOK {
		t.Fatalf("check: %d %v", code, out)
	}
	hosts := out["hosts"].([]any)
	local, missing := hosts[0].(map[string]any), hosts[1].(map[string]any)
	if missing["status"] != "skip" {
		t.Fatalf("unknown host: %v", missing)
	}
	if local["status"] != "changes" || len(local["files"].([]any)) == 0 {
		t.Fatalf("local: %v", local)
	}
	f := local["files"].([]any)[0].(map[string]any)
	if !strings.Contains(f["path"].(string), "jail.d/nkt-recidive.local") || !strings.Contains(f["after"].(string), "[recidive]") {
		t.Fatalf("file: %v", f)
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("no token")
	}
	code, out = call(srv.handleF2BTemplateApply, `{"token":"`+token+`"}`)
	if code != http.StatusOK {
		t.Fatalf("apply: %d %v", code, out)
	}
	jobID := int64(out["job_id"].(float64))
	deadline := time.Now().Add(30 * time.Second)
	var j store.Job
	for time.Now().Before(deadline) {
		if j, _ = db.JobByID(ctx, jobID); j.Status == store.JobSucceeded || j.Status == store.JobFailed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if j.Status != store.JobSucceeded {
		t.Fatalf("job: %s\n%s", j.Status, jobLogText(t, ctx, db, jobID))
	}
	if b, err := os.ReadFile(filepath.Join(root, "etc", "fail2ban", "jail.d", "nkt-recidive.local")); err != nil || !strings.Contains(string(b), "[recidive]") {
		t.Fatalf("jail not written: %q %v", b, err)
	}
	// Та же проверка второй раз — отказ.
	if code, _ := call(srv.handleF2BTemplateApply, `{"token":"`+token+`"}`); code != http.StatusConflict {
		t.Fatalf("token reused: %d", code)
	}
	// Неизвестный свой шаблон — 404.
	if code, _ := call(srv.handleF2BTemplateCheck, `{"name":"nope","host_ids":[-1]}`); code != http.StatusNotFound {
		t.Fatalf("unknown custom: %d", code)
	}
}
