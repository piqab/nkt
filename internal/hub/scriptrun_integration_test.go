package hub

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Сухой прогон сценария через настоящий SSH-туннель к nkt в режиме
// fixtures: план шагов-«мини-профилей» спрашивается у хоста, ожидания
// порта и HTTP проверяются хабом по-настоящему, а необратимые шаги только
// записываются в журнал. Ничего на «хосте» не меняется.
func TestScriptDryRunRoundTrip(t *testing.T) {
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)

	repoRoot := findRepoRoot(t)
	nktBin := filepath.Join(t.TempDir(), "nkt")
	buildCmd := exec.Command("go", "build", "-o", nktBin, "./cmd/nkt")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build nkt for the test: %v\n%s", err, out)
	}
	const adminPassword = "integration-test-password-1234"
	remoteCmd := exec.Command(nktBin)
	remoteCmd.Dir = repoRoot
	remoteCmd.Env = append(os.Environ(),
		"NKT_MODE=fixtures",
		"NKT_ADDR=127.0.0.1:8077",
		"NKT_DATA_DIR="+t.TempDir(),
		"NKT_BOOTSTRAP_ADMIN_USER=admin",
		"NKT_BOOTSTRAP_ADMIN_PASSWORD="+adminPassword,
		"NKT_COOKIE_SECURE=false",
		"NKT_SCHEDULER_ENABLED=false",
	)
	if err := remoteCmd.Start(); err != nil {
		t.Fatalf("start remote nkt: %v", err)
	}
	t.Cleanup(func() {
		_ = remoteCmd.Process.Kill()
		_, _ = remoteCmd.Process.Wait()
	})
	waitForLocalHTTP(t, "http://127.0.0.1:8077/api/health")

	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("open hub store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	me, err := osuser.Current()
	if err != nil {
		t.Fatal(err)
	}
	secretEnc, _ := secretbox.Encrypt(key, clientKeyPEM)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hostID, err := db.CreateHost(ctx, "web1", sshAddr, sshPort, me.Username, store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatal(err)
	}
	adminEnc, _ := secretbox.Encrypt(key, []byte(adminPassword))
	if err := db.SetHostAdmin(ctx, hostID, "admin", adminEnc); err != nil {
		t.Fatal(err)
	}
	if err := db.SetHostStatus(ctx, hostID, store.HostStatusOnline, ""); err != nil {
		t.Fatal(err)
	}

	m := NewManager(&config.Config{}, db, key, "test", slog.New(slog.DiscardHandler))
	s := &Server{hub: m, db: db, jobs: jobs.New(db, slog.New(slog.DiscardHandler))}
	r := NewScriptRunner(s)
	s.jobs.Register(KindScriptRun, r)

	content := strings.Join([]string{
		`param PKG "пакет"`,
		"host web2 192.0.2.11 user root password ask",
		"on web1 web2 packages install ${PKG}",
		"wait web1 port " + itoa(sshPort) + " 10s",
		"wait web1 http http://127.0.0.1:8077/api/health 200 10s",
		"on web1 user add deploy sudo",
		"on web1 system timezone Europe/Moscow",
		"on web1 cert issue example.org",
		"on web1 git clone https://github.com/org/app.git /srv/app",
	}, "\n")
	ticket := r.keep(map[string]string{"param:PKG": "zzz-not-installed", "host:web2": "x"})
	id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindScriptRun, Title: "dry", Queue: "script:1", Steps: 9,
		Params: ScriptRunParams{ScriptID: 1, Name: "dry", Content: content, Ticket: ticket, DryRun: true}})
	if err != nil {
		t.Fatal(err)
	}
	var job store.Job
	for {
		job, _ = db.JobByID(ctx, id)
		if job.Done() || ctx.Err() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if job.Status != store.JobSucceeded {
		t.Fatalf("статус %s: %s", job.Status, job.Error)
	}
	log := jobLogText(t, ctx, db, id)
	for _, want := range []string{
		"zzz-not-installed",    // план с хоста: пакет ставится
		"web2",                 // второй хост ещё не заведён
		"user add deploy sudo", // необратимое — только в журнал
		"cert issue example.org",
		"git clone https://github.com/org/app.git",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("в журнале нет %q:\n%s", want, log)
		}
	}
	// Ничего не завелось: сухой прогон не создаёт хосты.
	hosts, _ := db.ListHosts(ctx)
	if len(hosts) != 1 {
		t.Errorf("хостов %d после сухого прогона: %+v", len(hosts), hosts)
	}

	// Настоящие ожидания: открытый порт и ответ HTTP хаб проверяет сам,
	// закрытый порт проваливается по таймауту с указанием строки.
	run := func(n int, content string) store.Job {
		id, err := s.jobs.Start(ctx, jobs.Spec{Kind: KindScriptRun, Title: "wait", Queue: "script:" + itoa(n), Steps: 1,
			Params: ScriptRunParams{ScriptID: int64(n), Name: "wait", Content: content}})
		if err != nil {
			t.Fatal(err)
		}
		for {
			job, _ := db.JobByID(ctx, id)
			if job.Done() || ctx.Err() != nil {
				return job
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	job = run(2, "wait web1 port "+itoa(sshPort)+" 10s\nwait web1 http http://127.0.0.1:8077/api/health 200 10s")
	if job.Status != store.JobSucceeded {
		t.Errorf("ожидание порта и HTTP: статус %s: %s", job.Status, job.Error)
	}
	job = run(3, "wait web1 port 1 2s")
	if job.Status != store.JobFailed || !strings.Contains(job.Error, "строка 1") {
		t.Errorf("ожидался провал wait на строке 1, статус %s: %s", job.Status, job.Error)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func jobLogText(t *testing.T, ctx context.Context, db *store.DB, id int64) string {
	t.Helper()
	lines, err := db.JobLog(ctx, id, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}
