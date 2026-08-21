package collect

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func fixturesRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestFixturesSimulatesEnableDisable is the fixtures-layer counterpart to
// control.TestServiceActionEnableDisable — locks in that "systemctl enable/
// disable" is stateful here the same way start/stop already is: a later
// "systemctl show" must reflect whichever one ran last, not always answer
// with the canned file's original UnitFileState.
func TestFixturesSimulatesEnableDisable(t *testing.T) {
	f := NewFixtures(fixturesRoot(t))
	ctx := context.Background()

	show := func() string {
		res, err := f.Run(ctx, "systemctl", "show", "nginx", "-p", "Description")
		if err != nil {
			t.Fatalf("systemctl show: %v", err)
		}
		return res.Stdout
	}

	// Fixture's canned file (systemctl_show_nginx.txt) starts UnitFileState=enabled.
	if !strings.Contains(show(), "UnitFileState=enabled") {
		t.Fatalf("initial show output = %q, want UnitFileState=enabled from the canned fixture", show())
	}

	if res, err := f.Run(ctx, "systemctl", "disable", "nginx"); err != nil || !res.OK() {
		t.Fatalf("systemctl disable: res=%+v err=%v", res, err)
	}
	if got := show(); !strings.Contains(got, "UnitFileState=disabled") {
		t.Errorf("show after disable = %q, want UnitFileState=disabled", got)
	}

	if res, err := f.Run(ctx, "systemctl", "enable", "nginx"); err != nil || !res.OK() {
		t.Fatalf("systemctl enable: res=%+v err=%v", res, err)
	}
	if got := show(); !strings.Contains(got, "UnitFileState=enabled") {
		t.Errorf("show after enable = %q, want UnitFileState=enabled", got)
	}

	// ActiveState/SubState must stay whatever the canned file already had —
	// enable/disable toggling must not accidentally also flip run state.
	if got := show(); !strings.Contains(got, "ActiveState=active") {
		t.Errorf("show after enable = %q, want ActiveState still active (untouched by enable/disable)", got)
	}
}

func TestFixturesIsEnabled(t *testing.T) {
	f := NewFixtures(fixturesRoot(t))
	ctx := context.Background()

	if _, err := f.Run(ctx, "systemctl", "disable", "nginx"); err != nil {
		t.Fatalf("systemctl disable: %v", err)
	}
	res, err := f.Run(ctx, "systemctl", "is-enabled", "nginx")
	if err != nil {
		t.Fatalf("systemctl is-enabled: %v", err)
	}
	if got := strings.TrimSpace(res.Stdout); got != "disabled" {
		t.Errorf("is-enabled after disable = %q, want disabled", got)
	}
}
