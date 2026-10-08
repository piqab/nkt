package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
)

func TestArchiveFetchRequest(t *testing.T) {
	sum := hex.EncodeToString(make([]byte, 32))
	ok := []struct{ engine, url, name, checksum, want string }{
		{"docker", "https://ex.org/img/app.tar", "", "", "docker__app.tar"},
		{"podman", "http://ex.org/app.tar.gz?token=1", "", sum, "podman__app.tar.gz"},
		{"", "https://ex.org/x", "my.tar.zst", "", "docker__my.tar.zst"},
		{"docker", "https://ex.org/a.tar", "docker__b.tgz", "", "docker__b.tgz"},
	}
	for _, c := range ok {
		p, err := archiveFetchRequest(c.engine, c.url, c.name, c.checksum, true)
		if err != nil || p.Name != c.want {
			t.Errorf("%v: %q, %v (ждали %q)", c, p.Name, err, c.want)
		}
	}
	bad := []struct{ engine, url, name, checksum string }{
		{"lxd", "https://ex.org/a.tar", "", ""},            // только docker и podman
		{"docker", "ftp://ex.org/a.tar", "", ""},           // не http(s)
		{"docker", "file:///etc/passwd.tar", "", ""},       // не http(s)
		{"docker", "https:///a.tar", "", ""},               // без хоста
		{"docker", "https://ex.org/a.squashfs", "", ""},    // не для docker load
		{"docker", "https://ex.org/a.tar", "../x.tar", ""}, // выход из каталога
		{"docker", "https://ex.org/a.tar", "a b.tar", ""},  // пробел в имени
		{"docker", "https://ex.org/a.tar", "", "abc"},      // не sha256
	}
	for _, c := range bad {
		if _, err := archiveFetchRequest(c.engine, c.url, c.name, c.checksum, false); err == nil {
			t.Errorf("%v: принято", c)
		}
	}
}

// Задание качает архив в каталог архивов и сверяет сумму; неверная сумма —
// ошибка, и битый кусок не остаётся. Готовый архив с тем же именем не
// перезаписывается.
func TestArchiveFetchJob(t *testing.T) {
	body := bytes.Repeat([]byte("nkt image layer "), 4096)
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "app.tar", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	dir := t.TempDir()
	s := &Server{db: db, jobs: jm, cfg: &config.Config{}, images: control.NewImageManager(nil, nil, dir)}
	jm.Register(KindArchiveFetch, &archiveFetchRunner{s})

	post := func(req map[string]any) (int, int64) {
		b, _ := json.Marshal(req)
		rec := httptest.NewRecorder()
		s.handleImageArchiveFetch(rec, httptest.NewRequest("POST", "/images/archives/fetch", bytes.NewReader(b)))
		var out struct {
			JobID int64 `json:"job_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.JobID
	}
	wait := func(id int64) store.Job {
		for i := 0; i < 200; i++ {
			j, err := db.JobByID(t.Context(), id)
			if err == nil && (j.Status == store.JobSucceeded || j.Status == store.JobFailed) {
				return j
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("задание %d не закончилось", id)
		return store.Job{}
	}

	code, id := post(map[string]any{"engine": "docker", "url": srv.URL + "/app.tar", "checksum": hex.EncodeToString(sum[:]), "load": false})
	if code != 200 || id == 0 {
		t.Fatalf("запуск: %d", code)
	}
	if j := wait(id); j.Status != store.JobSucceeded {
		t.Fatalf("задание: %s %s", j.Status, j.Error)
	}
	got, err := os.ReadFile(filepath.Join(dir, "docker__app.tar"))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("архив: %d байт, %v", len(got), err)
	}

	if code, _ := post(map[string]any{"engine": "docker", "url": srv.URL + "/app.tar"}); code != http.StatusConflict {
		t.Errorf("повтор того же имени: %d, ждали 409", code)
	}

	code, id = post(map[string]any{"engine": "podman", "url": srv.URL + "/app.tar", "checksum": hex.EncodeToString(make([]byte, 32))})
	if code != 200 {
		t.Fatalf("запуск с неверной суммой: %d", code)
	}
	if j := wait(id); j.Status != store.JobFailed {
		t.Errorf("неверная сумма: %s", j.Status)
	}
	for _, f := range []string{"podman__app.tar", "podman__app.tar.part"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("остался %s", f)
		}
	}
}

// Загрузка с компьютера с ?load=1: как только файл получен, сервер сам
// заводит задание docker load — дальше от окна браузера ничего не зависит.
func TestArchiveUploadStartsLoadJob(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	jm := jobs.New(db, slog.New(slog.DiscardHandler))
	t.Cleanup(jm.Close)
	release := make(chan struct{})
	defer close(release)
	jm.Register(KindHostOp, blockRunner{release})
	dir := t.TempDir()
	s := &Server{db: db, jobs: jm, cfg: &config.Config{}, images: control.NewImageManager(nil, nil, dir)}

	upload := func(query string) map[string]any {
		rec := httptest.NewRecorder()
		s.handleImageArchiveUpload(rec, httptest.NewRequest("PUT", "/images/archives/upload?"+query, bytes.NewReader([]byte("tar"))))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %v", query, rec.Code, out)
		}
		return out
	}
	loadArgs := func(out map[string]any) archiveLoadArgs {
		t.Helper()
		id, _ := out["job_id"].(float64)
		if id == 0 {
			t.Fatalf("нет задания: %v", out)
		}
		j, err := db.JobByID(t.Context(), int64(id))
		if err != nil {
			t.Fatal(err)
		}
		var p HostOpParams
		_ = json.Unmarshal([]byte(j.Params), &p)
		var a archiveLoadArgs
		_ = json.Unmarshal(p.Args, &a)
		if p.Op != "archive.load" {
			t.Fatalf("операция: %q", p.Op)
		}
		return a
	}
	a := loadArgs(upload("name=podman__app.tar&load=1"))
	if a.Engine != "podman" || a.Path != filepath.Join(dir, "podman__app.tar") || a.Remove {
		t.Errorf("load: %+v", a)
	}
	// «Удалить архив после загрузки» — remove=1.
	if a := loadArgs(upload("name=docker__rm.tar&load=1&remove=1")); !a.Remove {
		t.Errorf("remove не передан: %+v", a)
	}
	if out := upload("name=docker__b.tar"); out["job_id"] != nil {
		t.Errorf("без load задание не нужно: %v", out)
	}
	if out := upload("name=lxd__c.tar.gz&load=1"); out["job_id"] != nil {
		t.Errorf("LXD не загружается load: %v", out)
	}
}

// Удалить архив после загрузки — только вместе с загрузкой в движок:
// без load архив и есть результат, его не трогают.
func TestUploadRemoveOnlyWithLoad(t *testing.T) {
	s := &Server{cfg: &config.Config{}, images: control.NewImageManager(nil, nil, t.TempDir())}
	for _, c := range []struct {
		p    UploadParams
		want bool
	}{
		{UploadParams{Target: uploadTargetArchive, Name: "docker__a.tar", Load: true, Remove: true}, true},
		{UploadParams{Target: uploadTargetArchive, Name: "docker__a.tar", Remove: true}, false},
		{UploadParams{Target: uploadTargetArchive, Name: "lxd__a.tar.gz", Load: true, Remove: true}, false},
	} {
		p := c.p
		if err := s.validateUpload(&p); err != nil {
			t.Fatalf("%+v: %v", c.p, err)
		}
		if p.Remove != c.want {
			t.Errorf("%+v: remove=%v, ждали %v", c.p, p.Remove, c.want)
		}
	}
}
