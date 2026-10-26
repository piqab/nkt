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
	"github.com/piqab/nkt/internal/store"
)

// Цикл машины через хаб (1.17.4) на локальном хосте fixtures: снимки в
// потолке токена, размеры в его потолках с учётом, диск только растёт,
// образ с диска хоста и чужие ключи в user-data — отказ, чужая машина —
// отказ, администратору — без потолков.
func TestVMLifecycleByToken(t *testing.T) {
	srv, db, _ := localFixtureHub(t)
	h := srv.Handler()
	ctx := context.Background()
	app1, _ := db.CreateHost(ctx, "app1", "10.0.1.1", 22, "deploy", store.HostAuthKey, []byte("x"))
	app9, _ := db.CreateHost(ctx, "app9", "10.0.1.9", 22, "deploy", store.HostAuthKey, []byte("x"))
	_ = db.SetHostParent(ctx, app1, LocalHostID)
	_ = db.SetHostParent(ctx, app9, LocalHostID)

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
	rec = do("POST", "/api/hub/tokens", `{"name":"client","role":"custom","perms":{"actions":["vm.manage"],
		"limits":{"vms":2,"vm_cpu":2,"vm_mem_mb":2048,"vm_disk_gb":20,"vm_snapshots":1}}}`, session)
	var c struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil || rec.Code != 200 {
		t.Fatalf("token: %d %s", rec.Code, rec.Body)
	}
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+c.Token) }
	_ = db.SetTokenUsage(ctx, store.TokenUsage{TokenID: c.ID, Kind: store.UsageVM, HostID: LocalHostID, Ref: "app1", CPU: 1, MemMB: 512, DiskGB: 10})
	vm := "/api/hub/hosts/" + strconv.FormatInt(app1, 10) + "/vm"
	expect := func(rec *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if rec.Code != code {
			t.Fatalf("%s: want %d, got %d %s", what, code, rec.Code, rec.Body)
		}
	}

	rec = do("GET", vm+"/snapshots", "", bearer)
	expect(rec, 200, "snapshots")
	if !strings.Contains(rec.Body.String(), "before") {
		t.Fatalf("snapshots: %s", rec.Body)
	}
	expect(do("POST", vm+"/snapshots", `{"name":"x"}`, bearer), 403, "snapshot over the limit")
	expect(do("POST", vm+"/snapshots", `{"name":"after"}`, session), 200, "admin snapshot")

	expect(do("POST", vm+"/resize", `{"vcpus":4}`, bearer), 403, "vcpus over the limit")
	if u, _ := srv.tokenVMUsage(ctx, store.APIToken{ID: c.ID}, store.Host{ParentID: LocalHostID, Name: "app1"}); u.CPU != 1 {
		t.Fatalf("usage after denied resize: %+v", u)
	}
	rec = do("POST", vm+"/resize", `{"vcpus":2,"memory_mb":1024}`, bearer)
	expect(rec, 200, "resize")
	if !strings.Contains(rec.Body.String(), `"restart_needed":true`) {
		t.Fatalf("resize: %s", rec.Body)
	}
	if u, _ := srv.tokenVMUsage(ctx, store.APIToken{ID: c.ID}, store.Host{ParentID: LocalHostID, Name: "app1"}); u.CPU != 2 || u.MemMB != 1024 || u.DiskGB != 10 {
		t.Fatalf("usage after resize: %+v", u)
	}
	expect(do("POST", vm+"/resize", `{"disk_gb":5}`, bearer), 400, "disk shrink")

	expect(do("POST", vm+"/reinstall", `{"image_id":"host:app9.qcow2"}`, bearer), 403, "host image")
	expect(do("POST", vm+"/reinstall", `{"image_id":"ubuntu-24.04","user_data":"#cloud-config\nusers: []\n"}`, bearer), 400, "reserved user-data")
	expect(do("POST", vm+"/reinstall", `{"image_id":"ubuntu-24.04","user_data":"#!/bin/sh\nid\n"}`, bearer), 400, "script user-data")

	// Чужая машина — ни снимков, ни удаления.
	other := "/api/hub/hosts/" + strconv.FormatInt(app9, 10) + "/vm"
	expect(do("GET", other+"/snapshots", "", bearer), 403, "foreign snapshots")
	expect(do("DELETE", other, "", bearer), 403, "foreign delete")
	expect(do("POST", other+"/reboot", "", bearer), 403, "foreign reboot")
}
