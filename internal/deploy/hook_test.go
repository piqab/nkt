package deploy

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sign(secret string, data []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(data)
	return hex.EncodeToString(m.Sum(nil))
}

func TestVerifyHook(t *testing.T) {
	now := time.Unix(1790000000, 0)
	body := []byte(`{"ref":"refs/heads/main","after":"` + strings.Repeat("a", 40) + `"}`)
	gh := http.Header{}
	gh.Set("X-Hub-Signature-256", "sha256="+sign("s3", body))
	gh.Set("X-GitHub-Delivery", "d-1")
	gh.Set("X-GitHub-Event", "push")
	ev, err := VerifyHook(gh, body, "s3", now)
	if err != nil || ev.Provider != "github" || ev.Delivery != "github:d-1" || ev.Ref != "refs/heads/main" || ev.Commit != strings.Repeat("a", 40) {
		t.Fatalf("github: %+v %v", ev, err)
	}
	if _, err := VerifyHook(gh, body, "other", now); err == nil {
		t.Error("чужой секрет принят")
	}
	if _, err := VerifyHook(gh, append(body, ' '), "s3", now); err == nil {
		t.Error("изменённое тело принято")
	}
	gt := http.Header{}
	gt.Set("X-Gitea-Signature", sign("s3", body))
	if ev, err := VerifyHook(gt, body, "s3", now); err != nil || ev.Provider != "gitea" || !strings.HasPrefix(ev.Delivery, "gitea:") {
		t.Errorf("gitea: %+v %v", ev, err)
	}
	gl := http.Header{}
	gl.Set("X-Gitlab-Token", "s3")
	if _, err := VerifyHook(gl, body, "s3", now); err != nil {
		t.Errorf("gitlab: %v", err)
	}
	gl.Set("X-Gitlab-Token", "s4")
	if _, err := VerifyHook(gl, body, "s3", now); err == nil {
		t.Error("gitlab: чужой токен принят")
	}
	nb := []byte(`{"tag":"v1.2.3"}`)
	ts := strconv.FormatInt(now.Unix(), 10)
	nk := http.Header{}
	nk.Set("X-NKT-Timestamp", ts)
	nk.Set("X-NKT-Signature", sign("s3", []byte(ts+"."+string(nb))))
	if ev, err := VerifyHook(nk, nb, "s3", now); err != nil || ev.Tag != "v1.2.3" {
		t.Errorf("nkt: %+v %v", ev, err)
	}
	if _, err := VerifyHook(nk, nb, "s3", now.Add(10*time.Minute)); err == nil {
		t.Error("nkt: устаревший запрос принят")
	}
	if _, err := VerifyHook(http.Header{}, body, "s3", now); err == nil {
		t.Error("без подписи принят")
	}
	bad := []byte(`{"tag":"v1; rm -rf /"}`)
	nk.Set("X-NKT-Signature", sign("s3", []byte(ts+"."+string(bad))))
	if _, err := VerifyHook(nk, bad, "s3", now); err == nil {
		t.Error("мусор в теге принят")
	}
}

func TestDecide(t *testing.T) {
	spec := Spec{Ref: "main", Tags: "v*"}
	sha := strings.Repeat("b", 40)
	cases := []struct {
		ev   HookEvent
		want Decision
	}{
		{HookEvent{Ref: "refs/heads/main", Commit: sha}, Decision{Deploy: true, Ref: "main", Commit: sha}},
		{HookEvent{Ref: "refs/heads/dev"}, Decision{Reason: "deploy.hookOtherBranch"}},
		{HookEvent{Ref: "refs/tags/v2.0.0", Commit: sha}, Decision{Deploy: true, Ref: "v2.0.0", Commit: sha, Tag: "v2.0.0"}},
		{HookEvent{Ref: "refs/tags/nightly"}, Decision{Reason: "deploy.hookOtherTag"}},
		{HookEvent{Tag: "1.4.2"}, Decision{Deploy: true, Ref: "main", Tag: "1.4.2"}},
		{HookEvent{Ref: "refs/heads/main", Deleted: true}, Decision{Reason: "deploy.hookDeleted"}},
	}
	for _, c := range cases {
		if got := Decide(spec, c.ev); got != c.want {
			t.Errorf("%+v → %+v, want %+v", c.ev, got, c.want)
		}
	}
}

func TestRegistryTags(t *testing.T) {
	if got := NewestTag([]string{"latest", "v1.9.3", "v1.10.0", "v1.10.0-rc1", "sha-abc"}, ""); got != "v1.10.0" {
		t.Errorf("новейший: %s", got)
	}
	for img, want := range map[string]string{"nginx": "https://registry-1.docker.io library/nginx", "org/app": "https://registry-1.docker.io org/app",
		"ghcr.io/org/app": "https://ghcr.io org/app", "docker.io/redis": "https://registry-1.docker.io library/redis", "reg.local:5000/a/b": "https://reg.local:5000 a/b"} {
		b, r := registryBase(img)
		if b+" "+r != want {
			t.Errorf("%s → %s %s", img, b, r)
		}
	}
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if u, p, ok := r.BasicAuth(); !ok || u != "bot" || p != "pat" || r.URL.Query().Get("scope") != "repository:org/app:pull" {
				w.WriteHeader(401)
				return
			}
			fmt.Fprint(w, `{"token":"T"}`)
		case r.Header.Get("Authorization") != "Bearer T":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="reg",scope="repository:org/app:pull"`)
			w.WriteHeader(401)
		case r.URL.Query().Get("last") == "":
			w.Header().Set("Link", `</v2/org/app/tags/list?n=1000&last=v1.1.0>; rel="next"`)
			fmt.Fprint(w, `{"tags":["v1.0.0","v1.1.0"]}`)
		default:
			fmt.Fprint(w, `{"tags":["v1.2.0"]}`)
		}
	}))
	defer srv.Close()
	old := registryClient
	registryClient = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	defer func() { registryClient = old }()
	host := strings.TrimPrefix(srv.URL, "https://")
	tags, err := ListTags(context.Background(), host+"/org/app", RegistryAccess{Cred: "bot:pat"})
	if err != nil || strings.Join(tags, ",") != "v1.0.0,v1.1.0,v1.2.0" {
		t.Fatalf("теги: %v %v", tags, err)
	}
	if _, err := ListTags(context.Background(), host+"/org/app", RegistryAccess{Cred: "bot:wrong"}); err == nil {
		t.Error("неверный доступ принят")
	}
}
