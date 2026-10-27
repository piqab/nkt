package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Машины токенов (1.17.14): полоса и диск — свои потолки или значения хаба;
// снимок резервирует диск машины в лимите диска токена, удаление снимка
// снимает резерв; сверка не трогает экземпляры LXD, которые есть, и
// убирает пропавшие.
func TestTokenMachineLimits(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	ctx := context.Background()

	d := vmLimitDefaults(ctx, db)
	if d != vmLimitFallback {
		t.Fatalf("fallback: %+v", d)
	}
	_ = db.KVSet(ctx, vmLimitsKV, `{"net_mbit":50,"iops":500,"disk_mbps":20}`)
	spec := vmcreate.Spec{}
	applyTokenVMLimits(store.APIToken{Perms: store.TokenPerms{Limits: store.TokenLimits{VMNetMbit: 200}}}, vmLimitDefaults(ctx, db), &spec)
	if spec.NetMbit != 200 || spec.IOPS != 500 || spec.DiskMBps != 20 {
		t.Fatalf("limits: %+v", spec)
	}
	cmds, err := control.LXDLimitArgs("c1", control.LXDLaunchOpts{NetMbit: 100, DiskMBps: 50, DiskGB: 10})
	if err != nil || len(cmds) != 2 || !strings.Contains(strings.Join(cmds[0], " "), "limits.egress=100Mbit") || cmds[1][2] != "set" {
		t.Fatalf("lxd limits: %v %v", cmds, err)
	}

	// Снимки в лимите диска.
	h := srv.Handler()
	app1, _ := db.CreateHost(ctx, "app1", "10.0.1.1", 22, "deploy", store.HostAuthKey, []byte("x"))
	_ = db.SetHostParent(ctx, app1, LocalHostID)
	do := func(method, path, body string, set func(*http.Request)) *httptest.ResponseRecorder {
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
	rec = do("POST", "/api/hub/tokens", `{"name":"client","role":"custom","perms":{"actions":["vm.manage"],"limits":{"vm_snapshots":5,"disk_gb":30}}}`, session)
	var c struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageVM, HostID: LocalHostID, Ref: "app1", CPU: 1, MemMB: 512, DiskGB: 20})
	vm := "/api/hub/hosts/" + strconv.FormatInt(app1, 10) + "/vm"
	if rec := do("POST", vm+"/snapshots", `{"name":"s1"}`, bearer); rec.Code != 403 {
		t.Fatalf("snapshot over the disk limit: %d %s", rec.Code, rec.Body)
	}
	tok, _ := db.APITokenByID(ctx, c.ID)
	tok.Perms.Limits.DiskGB = 50
	_ = db.UpdateAPIToken(ctx, tok)
	if rec := do("POST", vm+"/snapshots", `{"name":"s1"}`, bearer); rec.Code != 200 {
		t.Fatalf("snapshot: %d %s", rec.Code, rec.Body)
	}
	u, _ := db.ListTokenUsage(ctx, c.ID)
	var snap *store.TokenUsage
	for i := range u {
		if u[i].Kind == store.UsageSnapshot {
			snap = &u[i]
		}
	}
	if snap == nil || snap.Ref != "app1/s1" || snap.DiskGB != 20 {
		t.Fatalf("snapshot usage: %+v", u)
	}

	// Сверка: экземпляр LXD, который есть, остаётся; пропавший — уходит.
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageVM, HostID: LocalHostID, Ref: lxdRef("build-runner"), CPU: 1})
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageVM, HostID: LocalHostID, Ref: lxdRef("gone"), CPU: 1})
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageSnapshot, HostID: LocalHostID, Ref: lxdRef("gone") + "/s1", DiskGB: 5})
	srv.reconcileTokenUsage(ctx)
	refs := map[string]bool{}
	u, _ = db.ListTokenUsage(ctx, c.ID)
	for _, x := range u {
		refs[x.Ref] = true
	}
	if !refs["lxd/build-runner"] || refs["lxd/gone"] || refs["lxd/gone/s1"] {
		t.Fatalf("reconcile: %v", refs)
	}
}
