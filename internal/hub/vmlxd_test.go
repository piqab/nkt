package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/store"
)

// LXD для токена (1.17.7): создать — только с размерами в потолках, в сети
// токена; не создалось — учёт снят; список — только свои; снимки — в
// потолке; размеры — в потолках с учётом, диск только растёт; чужой
// экземпляр — отказ.
func TestLXDByToken(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	srv.jobs.Register(KindLXDCreate, NewLXDCreateRunner(srv))
	h := srv.Handler()
	ctx := context.Background()
	do := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		set(req)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("POST", "/api/auth/login", `{"username":"admin","password":"admin-password-1234"}`, func(*http.Request) {})
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookie {
			cookie = c
		}
	}
	session := func(r *http.Request) { r.AddCookie(cookie) }
	rec = do("POST", "/api/hub/tokens", `{"name":"client","role":"custom","perms":{"actions":["vm.manage"],"limits":{"vms":2,"vm_cpu":2,"vm_mem_mb":2048,"vm_disk_gb":20}}}`, session)
	var c struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != 200 {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	expect := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: want %d, got %d %s", what, code, rec.Code, rec.Body)
		}
	}
	base := "/api/hub/hosts/-1/lxd"
	expect(do("POST", base, `{"name":"c1","image":"ubuntu:24.04"}`, bearer), 403, "no sizes under limits")
	expect(do("POST", base, `{"name":"c1","image":"ubuntu:24.04","cpu":4,"mem_mb":1024,"disk_gb":10}`, bearer), 403, "cpu over the limit")
	expect(do("POST", base, `{"name":"1bad","image":"ubuntu:24.04","cpu":1,"mem_mb":1024,"disk_gb":10}`, bearer), 400, "bad name")
	rec = do("POST", base, `{"name":"c1","image":"ubuntu:24.04","cpu":1,"mem_mb":1024,"disk_gb":10}`, bearer)
	expect(rec, 200, "create")
	var job struct {
		JobID int64 `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &job)
	// На хосте fixtures сеть токена не завести — задание падает, учёт снят.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := db.JobByID(ctx, job.JobID); j.Done() {
			if j.Status == store.JobSucceeded {
				t.Fatal("created without a token network")
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if u, _ := db.ListTokenUsage(ctx, c.ID); len(u) != 0 {
		t.Fatalf("usage after a failed create: %+v", u)
	}

	// Список: свои — только свои.
	if b := do("GET", base, "", bearer).Body.String(); strings.Contains(b, "build-runner") {
		t.Fatalf("foreign instance listed: %s", b)
	}
	if b := do("GET", base, "", session).Body.String(); !strings.Contains(b, "build-runner") {
		t.Fatalf("admin list: %s", b)
	}
	expect(do("POST", base+"/build-runner/stop", "", bearer), 403, "foreign instance")
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageVM, HostID: localHostID, Ref: lxdRef("build-runner"), CPU: 1, MemMB: 512, DiskGB: 5})
	if b := do("GET", base, "", bearer).Body.String(); !strings.Contains(b, "build-runner") {
		t.Fatalf("own instance not listed: %s", b)
	}
	expect(do("POST", base+"/build-runner/snapshots", `{"name":"s1"}`, bearer), 403, "snapshots limit 0")
	expect(do("POST", base+"/build-runner/resize", `{"cpu":4}`, bearer), 403, "resize over the limit")
	expect(do("POST", base+"/build-runner/resize", `{"cpu":2}`, bearer), 200, "resize")
	if u, _ := db.ListTokenUsage(ctx, c.ID); len(u) != 1 || u[0].CPU != 2 || u[0].MemMB != 512 {
		t.Fatalf("usage after resize: %+v", u)
	}
	expect(do("POST", base+"/build-runner/resize", `{"disk_gb":2}`, bearer), 400, "disk shrink")
}

// Запуск экземпляра токена: сеть, потолки и явный запрет привилегий.
func TestLXDLaunchArgsOpts(t *testing.T) {
	args, err := control.LXDLaunchArgsOpts("ubuntu:24.04", "c1", true, control.LXDLaunchOpts{Network: "nkt-t5", CPU: 2, MemMB: 1024, DiskGB: 10, Isolated: true})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, want := range []string{"launch ubuntu:24.04 c1 --vm", "--network nkt-t5", "limits.cpu=2", "limits.memory=1024MiB", "root,size=10GiB", "security.privileged=false", "security.nesting=false"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in %s", want, got)
		}
	}
	for _, bad := range []control.LXDLaunchOpts{{Network: "Bad Net"}, {CPU: -1}, {MemMB: 64}} {
		if _, err := control.LXDLaunchArgsOpts("ubuntu:24.04", "c1", false, bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}
