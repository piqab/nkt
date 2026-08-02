package control

import (
	"context"
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
)

// argvRecordingCollector records every argv Run is called with, so a test
// can assert which command Write's apply step actually reaches for.
type argvRecordingCollector struct {
	collect.Collector
	calls [][]string
}

func (c *argvRecordingCollector) Run(ctx context.Context, name string, args ...string) (collect.CommandResult, error) {
	c.calls = append(c.calls, append([]string{name}, args...))
	return c.Collector.Run(ctx, name, args...)
}

func TestWriteAppliesDockerComposeUpD(t *testing.T) {
	root := copyFixturesRoot(t)
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	m := configsSetupWithCollector(t, root, rec)

	content, err := m.Read(composeFile)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := m.Write(context.Background(), "test", composeFile, content.Content, "", true); err != nil {
		t.Fatalf("Write: %v", err)
	}

	found := false
	for _, argv := range rec.calls {
		if strings.Join(argv, " ") == strings.Join([]string{"docker", "compose", "-f", composeFile, "up", "-d"}, " ") {
			found = true
		}
	}
	if !found {
		t.Errorf("docker compose -f %s up -d не вызван, звонки: %v", composeFile, rec.calls)
	}
}

func TestWriteAppliesNginxReloadNotComposeUp(t *testing.T) {
	root := copyFixturesRoot(t)
	rec := &argvRecordingCollector{Collector: collect.NewFixtures(root)}
	m := configsSetupWithCollector(t, root, rec)

	content, err := m.Read(nginxSiteFile)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if _, err := m.Write(context.Background(), "test", nginxSiteFile, content.Content, "", true); err != nil {
		t.Fatalf("Write: %v", err)
	}

	sawReload, sawComposeUp := false, false
	for _, argv := range rec.calls {
		joined := strings.Join(argv, " ")
		if joined == "systemctl reload nginx" {
			sawReload = true
		}
		if strings.Contains(joined, "compose") {
			sawComposeUp = true
		}
	}
	if !sawReload {
		t.Errorf("ожидался systemctl reload nginx, звонки: %v", rec.calls)
	}
	if sawComposeUp {
		t.Error("nginx не должен вызывать docker compose")
	}
}

// The fixtures have no canned "docker compose up -d" success response — the
// apply step fails (exit 127, "not found"), and that must stay visible in
// the result instead of silently vanishing, while the write itself — which
// did succeed — must not be reported as failed.
func TestWriteSurfacesApplyFailureWithoutFailingWrite(t *testing.T) {
	m := configsSetup(t)
	content, err := m.Read(composeFile)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	res, err := m.Write(context.Background(), "test", composeFile, content.Content, "", true)
	if err != nil {
		t.Fatalf("Write должен завершиться успешно, даже если apply не удался: %v", err)
	}
	if res.Applied {
		t.Error("Applied не должен быть true — нет заготовленного успешного ответа docker compose")
	}
	if !strings.Contains(res.Message, "Применить не удалось") {
		t.Errorf("Message = %q, ожидалось упоминание неудачного применения", res.Message)
	}
}
