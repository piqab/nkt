package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// Шаблон на хосты: проверка обязательна (без неё и повторно по той же
// проверке — отказ), применяется ровно проверенное.
func TestF2BTemplateCheckThenApply(t *testing.T) {
	srv, db, root := localFixtureHub(t)
	srv.jobs.Register(KindF2BTemplate, NewF2BTemplateRunner(srv))
	srv.jobs.Register(KindF2BTemplateCheck, NewF2BTemplateCheckRunner(srv))
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
	checkJob := int64(out["job_id"].(float64))
	waitJob := func(id int64) store.Job {
		deadline := time.Now().Add(30 * time.Second)
		var j store.Job
		for time.Now().Before(deadline) {
			if j, _ = db.JobByID(ctx, id); j.Status == store.JobSucceeded || j.Status == store.JobFailed {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		return j
	}
	if j := waitJob(checkJob); j.Status != store.JobSucceeded || j.Queue != F2BQueue {
		t.Fatalf("check job: %s %q\n%s", j.Status, j.Queue, jobLogText(t, ctx, db, checkJob))
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("job", strconv.FormatInt(checkJob, 10))
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	srv.handleF2BTemplateCheckResult(w, req)
	out = nil
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["done"] != true {
		t.Fatalf("check result: %d %v", w.Code, out)
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
	if j := waitJob(jobID); j.Status != store.JobSucceeded {
		t.Fatalf("job: %s\n%s", j.Status, jobLogText(t, ctx, db, jobID))
	}
	if b, err := os.ReadFile(filepath.Join(root, "etc", "fail2ban", "jail.d", "nkt-recidive.local")); err != nil || !strings.Contains(string(b), "[recidive]") {
		t.Fatalf("jail not written: %q %v", b, err)
	}
	// Та же проверка второй раз — отказ.
	if code, _ := call(srv.handleF2BTemplateApply, `{"token":"`+token+`"}`); code != http.StatusConflict {
		t.Fatalf("token reused: %d", code)
	}
	if j, _ := db.JobByID(ctx, jobID); j.Queue != F2BQueue {
		t.Fatalf("apply queue %q", j.Queue)
	}
	// Неизвестный свой шаблон — 404.
	if code, _ := call(srv.handleF2BTemplateCheck, `{"name":"nope","host_ids":[-1]}`); code != http.StatusNotFound {
		t.Fatalf("unknown custom: %d", code)
	}
}
