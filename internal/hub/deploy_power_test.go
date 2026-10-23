package hub

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// «Остановить» и «Запустить» выкладку: задание хаба по хостам выкладки;
// остановка ставит пометку (кто, когда, контейнеры), запуск её снимает;
// конвейер без выкладки — ошибка до задания.
func TestPipelinePower(t *testing.T) {
	srv, db, root := localFixtureHub(t)
	ctx := context.Background()
	content := "repo: https://codeberg.org/me/shop.git\nref: main\naction: compose\ncompose:\n  file: deploy/compose.yaml\n  project: shop\n  hosts: [localhost]\n"
	secret, _ := secretbox.Encrypt(srv.hub.key, []byte("s"))
	pid, err := db.CreatePipeline(ctx, store.Pipeline{Name: "shop", Content: content, HookID: "hook-power-1", HookSecret: secret, Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := db.PipelineByID(ctx, pid)
	if _, err := srv.startPipelinePower(ctx, pl, "stop", "admin"); err == nil || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("not deployed: %v", err)
	}
	dir := filepath.Join(root, "srv", "compose", "shop")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx:1.27\n"), 0o644)
	srv.saveDeployed(ctx, pid, deployedStack{Project: "shop", Hosts: []deployedHost{{ID: localHostID, Name: "localhost"}}})

	jid, err := srv.startPipelinePower(ctx, pl, "stop", "admin")
	if err != nil {
		t.Fatal(err)
	}
	j := waitJobDone(t, db, jid)
	log := jobLogText(t, ctx, db, jid)
	if j.Status != store.JobSucceeded || !strings.Contains(log, "stop") {
		t.Fatalf("stop: %+v\n%s", j, log)
	}
	st := srv.loadStopped(ctx, pid)
	if st == nil || st.By != "admin" || st.Project != "shop" || len(st.Hosts) != 1 {
		t.Fatalf("stopped mark: %+v", st)
	}
	// Цель Traffic Fly из контейнеров стека узнаётся как остановленная.
	st.Containers = map[string][]string{strconv.FormatInt(localHostID, 10): {"shop-web-1"}}
	srv.saveStopped(ctx, pid, st)
	if p, _ := srv.stoppedTarget(ctx, localHostID, "shop-web-1"); p == nil || p.Name != "shop" {
		t.Fatalf("stopped target: %+v", p)
	}
	if p, _ := srv.stoppedTarget(ctx, localHostID, "other-web-1"); p != nil {
		t.Fatalf("foreign container: %+v", p)
	}

	jid, err = srv.startPipelinePower(ctx, pl, "start", "admin")
	if err != nil {
		t.Fatal(err)
	}
	j = waitJobDone(t, db, jid)
	log = jobLogText(t, ctx, db, jid)
	if j.Status != store.JobSucceeded || !strings.Contains(log, "образы стека на хосте") || !strings.Contains(log, "up -d") {
		t.Fatalf("start: %+v\n%s", j, log)
	}
	if st := srv.loadStopped(ctx, pid); st != nil {
		t.Fatalf("stopped mark left: %+v", st)
	}
}
