package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

func testManager(t *testing.T) (*Manager, string, *[][]string) {
	t.Helper()
	root := t.TempDir()
	var calls [][]string
	run := func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		calls = append(calls, argv)
		return collect.CommandResult{}, nil
	}
	m := NewManager([]string{root}, collect.NewLocal("", "", 0), run, nil, filepath.Join(root, ".nkt-tmp"))
	return m, root, &calls
}

// Проводник ходит только по корням: /etc, корень диска и путь с «..» —
// отказ, и удалить сам корень тоже нельзя.
func TestCheckAndDeleteRoot(t *testing.T) {
	m, root, calls := testManager(t)
	for _, bad := range []string{"/etc/passwd", "/", root + "/../x", "relative", root + "x"} {
		if _, err := m.Check(bad); err == nil {
			t.Errorf("Check(%q) принят", bad)
		}
	}
	if _, err := m.Check(root + "/sub/file"); err != nil {
		t.Errorf("Check внутри корня: %v", err)
	}
	if err := m.Delete(context.Background(), root); err == nil || !strings.Contains(err.Error(), "корень") {
		t.Errorf("удаление корня: %v", err)
	}
	if err := m.Delete(context.Background(), root+"/old"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := (*calls)[len(*calls)-1]; got[0] != "rm" || got[len(got)-1] != root+"/old" {
		t.Errorf("удаление ушло как %v", got)
	}
}

// Загрузка: файл падает во временный каталог данных и переносится на
// место командой снаружи песочницы; имя с «/» — отказ.
func TestUpload(t *testing.T) {
	m, root, calls := testManager(t)
	target, err := m.Upload(context.Background(), root+"/www", "site.tar.gz", strings.NewReader("data"))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if target != root+"/www/site.tar.gz" {
		t.Errorf("target = %q", target)
	}
	last := (*calls)[len(*calls)-1]
	if last[0] != "install" || last[len(last)-1] != target {
		t.Errorf("перенос ушёл как %v", last)
	}
	if _, err := m.Upload(context.Background(), root, "../evil", strings.NewReader("x")); err == nil {
		t.Error("имя с «/» принято")
	}
}

// Архив с путём наружу не распаковывается — zip slip ловится по списку
// файлов до распаковки.
func TestUnsafeArchivePath(t *testing.T) {
	if got := UnsafeArchivePath("a/b.txt\nc/../../etc/passwd\n"); got != "c/../../etc/passwd" {
		t.Errorf("UnsafeArchivePath = %q", got)
	}
	if got := UnsafeArchivePath("/etc/shadow\n"); got != "/etc/shadow" {
		t.Errorf("абсолютный путь не пойман: %q", got)
	}
	if got := UnsafeArchivePath("ok/one\nok/two\n"); got != "" {
		t.Errorf("безопасный архив отвергнут: %q", got)
	}
	for p, want := range map[string]string{"x.zip": "zip", "x.tar.gz": "tar", "x.tgz": "tar", "x.txt": "", "X.TAR.XZ": "tar"} {
		if got := ArchiveKind(p); got != want {
			t.Errorf("ArchiveKind(%q) = %q, want %q", p, got, want)
		}
	}
}

// Параметры clone: адрес с паролем внутри — отказ (он попал бы в журнал),
// git@host:path — принимается, имя каталога — из адреса.
func TestCloneParams(t *testing.T) {
	good := []string{"https://github.com/org/repo.git", "git@github.com:org/repo.git", "ssh://git@host/org/repo"}
	for _, u := range good {
		p := CloneParams{URL: u}
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	bad := CloneParams{URL: "https://user:token@github.com/org/repo.git"}
	if err := bad.Validate(); err == nil {
		t.Error("адрес с секретом принят")
	}
	if RepoDirName("https://github.com/org/my-repo.git") != "my-repo" || RepoDirName("git@gh:org/x") != "x" {
		t.Errorf("RepoDirName = %q / %q", RepoDirName("https://github.com/org/my-repo.git"), RepoDirName("git@gh:org/x"))
	}

	// Секрет живёт в памяти по билету и в параметры не попадает.
	m, root, _ := testManager(t)
	r := NewCloneRunner(m)
	p := CloneParams{URL: "https://github.com/org/repo.git", Dest: root + "/repo", Auth: AuthToken}
	if err := r.Prepare(&p, "s3cr3t"); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if p.Ticket == "" {
		t.Fatal("билет не выдан")
	}
	if secret, ok := r.secrets.take(p.Ticket); !ok || secret != "s3cr3t" {
		t.Errorf("секрет по билету = %q, %v", secret, ok)
	}
	if _, ok := r.secrets.take(p.Ticket); ok {
		t.Error("секрет выдан второй раз")
	}
}

// Deploy-ключ заводится один раз и не пересоздаётся.
func TestDeployKeyStable(t *testing.T) {
	m, _, _ := testManager(t)
	_, pub1, err := m.DeployKey()
	if err != nil {
		t.Fatal(err)
	}
	_, pub2, _ := m.DeployKey()
	if pub1 == "" || pub1 != pub2 || !strings.HasPrefix(pub1, "ssh-ed25519 ") {
		t.Errorf("ключ = %q / %q", pub1, pub2)
	}
	if st, err := os.Stat(filepath.Join(m.tmpDir, "deploy-key", "id_ed25519")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("приватный ключ: %v, права %v", err, st)
	}
}

func TestScrubHidesUserAndSecret(t *testing.T) {
	line := "fatal: https://x-access-token@github.com/o/r.git rejected token s3cr3t"
	got := scrub(line, "https://x-access-token@github.com/o/r.git", "https://github.com/o/r.git", "s3cr3t")
	if strings.Contains(got, "x-access-token") || strings.Contains(got, "s3cr3t") {
		t.Fatalf("scrub оставил секрет: %q", got)
	}
}
