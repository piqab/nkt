package control

import (
	"context"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/msgs"
)

func TestWriteCreatesNewFile(t *testing.T) {
	m := configsSetup(t)
	const path = "/etc/nginx/sites-enabled/newsite.conf"

	res, err := m.Write(context.Background(), msgs.RU, "test", path,
		"server {\n    listen 80;\n    server_name new.example.com;\n}\n", "новый сайт", false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.RolledBack {
		t.Fatalf("WriteResult = %+v, откат не ожидался", res)
	}

	content, err := m.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content.Path != path || content.Service != "nginx" {
		t.Errorf("Read = %+v, ожидался nginx-файл по пути %s", content.ManagedFile, path)
	}
}

func TestWriteRejectsNewFileOutsideAllowlist(t *testing.T) {
	m := configsSetup(t)
	if _, err := m.Write(context.Background(), msgs.RU, "test", "/etc/passwd", "root:x:0:0::/root:/bin/sh\n", "", false); err == nil {
		t.Error("ожидалась ошибка для пути вне разрешённых каталогов")
	}
}

// failingValidatorCollector forces every Run call to fail, so a test can
// exercise ConfigManager.Write's rollback path without depending on the
// fixtures' canned "nginx -t" response (which always reports success,
// content-blind, matching real argv only).
type failingValidatorCollector struct {
	collect.Collector
}

func (f *failingValidatorCollector) Run(ctx context.Context, name string, args ...string) (collect.CommandResult, error) {
	return collect.CommandResult{
		Argv: append([]string{name}, args...), ExitCode: 1, Stderr: "имитация ошибки валидации", Simulated: true,
	}, nil
}

// A brand-new file has no previous version to restore on a failed
// validation — Write must delete it instead of leaving a broken config on
// disk the host never actually had.
func TestWriteDeletesNewFileOnValidationFailure(t *testing.T) {
	root := copyFixturesRoot(t)
	c := &failingValidatorCollector{Collector: collect.NewFixtures(root)}
	m := configsSetupWithCollector(t, root, c)
	const path = "/etc/nginx/sites-enabled/broken.conf"

	_, err := m.Write(context.Background(), msgs.RU, "test", path, "server { this is not valid nginx", "", false)
	if err == nil {
		t.Fatal("ожидалась ошибка из-за проваленной валидации")
	}

	if c.Exists(path) {
		t.Error("новый файл должен быть удалён после проваленной валидации, а не остаться на диске")
	}
}

// An existing file's rollback path (restore previous content) must keep
// working exactly as before this change — only the "no previous version"
// branch is new.
func TestWriteRestoresExistingFileOnValidationFailure(t *testing.T) {
	root := copyFixturesRoot(t)
	c := &failingValidatorCollector{Collector: collect.NewFixtures(root)}
	m := configsSetupWithCollector(t, root, c)
	const path = "/etc/nginx/sites-enabled/app.example.com.conf"

	before, err := m.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	res, err := m.Write(context.Background(), msgs.RU, "test", path, "server { this is not valid nginx", "", false)
	if err == nil {
		t.Fatal("ожидалась ошибка из-за проваленной валидации")
	}
	if !res.RolledBack {
		t.Fatalf("WriteResult = %+v, ожидался откат", res)
	}

	after, err := m.Read(path)
	if err != nil {
		t.Fatalf("Read после отката: %v", err)
	}
	if after.Content != before.Content {
		t.Error("содержимое существующего файла не восстановлено после проваленной валидации")
	}
}
