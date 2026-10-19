package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/inventory"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

// Перенос на настоящем docker (NKT_DOCKER_TEST=1): compose-стек с томом и
// каталогом рядом — упаковка, «чистый» хост (стек и том убраны, каталог
// отодвинут), распаковка с подъёмом — данные на месте; откат убирает всё
// созданное.
func TestMigrateRealDocker(t *testing.T) {
	if os.Getenv("NKT_DOCKER_TEST") == "" {
		t.Skip("docker: NKT_DOCKER_TEST=1")
	}
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".cache", "nkt-migrate-test")
	_ = os.RemoveAll(base)
	wd := filepath.Join(base, "proj")
	_ = os.MkdirAll(filepath.Join(wd, "conf"), 0o755)
	_ = os.WriteFile(filepath.Join(wd, "conf", "a.txt"), []byte("conf-1\n"), 0o644)
	compose := "services:\n  web:\n    image: httpd:2.4-alpine\n    command: [\"sh\", \"-c\", \"test -f /data/x || echo hi > /data/x; sleep 3600\"]\n    volumes:\n      - data:/data\n      - ./conf:/conf\nvolumes:\n  data:\n"
	_ = os.WriteFile(filepath.Join(wd, "compose.yml"), []byte(compose), 0o644)
	dc := func(args ...string) {
		t.Helper()
		out, err := exec.Command("docker", append([]string{"compose", "-p", "nktmig", "--project-directory", wd, "-f", filepath.Join(wd, "compose.yml")}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("compose %v: %v\n%s", args, err, out)
		}
	}
	dc("up", "-d")
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", "nktmig-web-1").Run()
		_ = exec.Command("docker", "volume", "rm", "nktmig_data").Run()
		_ = os.RemoveAll(base)
	})
	time.Sleep(2 * time.Second)

	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{Mode: config.ModeLocal, CommandTimeout: 30 * time.Second}
	c := collect.NewLocal("", "", 30*time.Second)
	scanner := inventory.New(cfg, c, db)
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	s := &Server{db: db, jobs: jm, cfg: cfg, scanner: scanner, images: control.NewImageManager(c, scanner, filepath.Join(base, "archives"))}
	jm.Register(KindHostOp, &hostOpRunner{s: s})
	ctx := context.Background()

	plan, err := s.migratePlan(ctx, "nktmig-web-1")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != "compose" || plan.Project != "nktmig" || len(plan.Volumes) != 1 || plan.Volumes[0].Name != "nktmig_data" || len(plan.Paths) != 1 || plan.Paths[0].Path != wd || len(plan.Refuse) > 0 {
		t.Fatalf("plan: %+v", plan)
	}
	run := func(op string, args any) {
		t.Helper()
		raw, _ := json.Marshal(args)
		id, err := jm.Start(ctx, jobs.Spec{Kind: KindHostOp, Queue: "t", Author: "admin", Steps: 1, Params: HostOpParams{Op: op, Args: raw}})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			j, _ := db.JobByID(ctx, id)
			if j.Status == store.JobSucceeded {
				return
			}
			if j.Status == store.JobFailed {
				lines, _ := db.JobLog(ctx, id, 0, 1000)
				var b strings.Builder
				for _, l := range lines {
					b.WriteString(l.Text + "\n")
				}
				t.Fatalf("%s failed: %s\n%s", op, j.Error, b.String())
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("%s timeout", op)
	}
	const id = "00112233445566aa"
	run("migrate.pack", migratePackArgs{ID: id, Container: "nktmig-web-1", SaveImages: []string{"httpd:2.4-alpine"}})
	m, _, err := s.readManifest(id)
	if err != nil || len(m.Files) != 3 {
		t.Fatalf("manifest: %v %+v", err, m.Files)
	}
	// «Новый хост»: того стека, тома и каталога нет.
	dc("down", "-v")
	if err := os.Rename(wd, wd+".src"); err != nil {
		t.Fatal(err)
	}
	run("migrate.unpack", migrateIDArgs{ID: id})
	out, err := exec.Command("docker", "exec", "nktmig-web-1", "cat", "/data/x", "/conf/a.txt").CombinedOutput()
	if err != nil || string(out) != "hi\nconf-1\n" {
		t.Fatalf("after unpack: %v %q", err, out)
	}
	run("migrate.undo", migrateIDArgs{ID: id})
	if err := exec.Command("docker", "volume", "inspect", "nktmig_data").Run(); err == nil {
		t.Fatal("volume left after undo")
	}
	if _, err := os.Stat(wd); !os.IsNotExist(err) {
		t.Fatalf("workdir left after undo: %v", err)
	}
	// «Убрать старый» с данными: контейнеры, тома и каталоги; повтор по
	// уже убранному — не ошибка.
	run("migrate.unpack", migrateIDArgs{ID: id})
	rm := migrateRemoveArgs{Containers: []string{"nktmig-web-1"}, Volumes: []string{"nktmig_data"}, Paths: []string{wd}}
	run("migrate.remove", rm)
	if err := exec.Command("docker", "inspect", "nktmig-web-1").Run(); err == nil {
		t.Fatal("container left after remove")
	}
	if err := exec.Command("docker", "volume", "inspect", "nktmig_data").Run(); err == nil {
		t.Fatal("volume left after remove")
	}
	if _, err := os.Stat(wd); !os.IsNotExist(err) {
		t.Fatalf("workdir left after remove: %v", err)
	}
	run("migrate.remove", rm)
}
