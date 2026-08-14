package parse

import (
	"path/filepath"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// TestClassifyCgroup covers the distinction the "Разное" inventory exists
// to make — a managed unit versus a container versus something a person
// started by hand — across both cgroup layouts, since v1 and v2 hosts are
// both still common and their files look nothing alike.
func TestClassifyCgroup(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantUnit   string
		wantCID    string
		wantOrigin string
	}{
		{
			name:       "cgroup v2 systemd service",
			content:    "0::/system.slice/nginx.service\n",
			wantUnit:   "nginx.service",
			wantOrigin: model.OriginService,
		},
		{
			name: "cgroup v1 systemd service",
			content: "12:pids:/system.slice/ssh.service\n" +
				"11:memory:/system.slice/ssh.service\n" +
				"1:name=systemd:/system.slice/ssh.service\n",
			wantUnit:   "ssh.service",
			wantOrigin: model.OriginService,
		},
		{
			name:       "templated unit keeps its instance name",
			content:    "0::/system.slice/system-getty.slice/getty@tty1.service\n",
			wantUnit:   "getty@tty1.service",
			wantOrigin: model.OriginService,
		},
		{
			name:       "interactive login session is a manual start",
			content:    "0::/user.slice/user-1000.slice/session-3.scope\n",
			wantOrigin: model.OriginManual,
		},
		{
			name:       "docker container scope under system.slice",
			content:    "0::/system.slice/docker-3f4a9c1b2d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8.scope\n",
			wantCID:    "3f4a9c1b2d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8",
			wantOrigin: model.OriginContainer,
		},
		{
			name:       "docker cgroup v1 path form",
			content:    "1:name=systemd:/docker/9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b\n",
			wantCID:    "9a8b7c6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b",
			wantOrigin: model.OriginContainer,
		},
		{
			name:       "podman container",
			content:    "0::/machine.slice/libpod-1122334455667788990011223344556677889900112233445566778899001122.scope\n",
			wantCID:    "1122334455667788990011223344556677889900112233445566778899001122",
			wantOrigin: model.OriginContainer,
		},
		{
			name:       "lxd payload",
			content:    "0::/lxc.payload.web01/system.slice/nginx.service\n",
			wantCID:    "web01",
			wantOrigin: model.OriginContainer,
		},
		{
			// The docker daemon itself is an ordinary service, and must not
			// be mistaken for a container just because "docker" appears in
			// the path — ".service" is not hex, which is what keeps these apart.
			name:       "docker daemon is a service, not a container",
			content:    "0::/system.slice/docker.service\n",
			wantUnit:   "docker.service",
			wantOrigin: model.OriginService,
		},
		{
			name:    "unrecognised layout yields nothing rather than a guess",
			content: "0::/\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			unit, cid, origin := classifyCgroup(c.content)
			if unit != c.wantUnit {
				t.Errorf("unit = %q, want %q", unit, c.wantUnit)
			}
			if cid != c.wantCID {
				t.Errorf("containerID = %q, want %q", cid, c.wantCID)
			}
			if origin != c.wantOrigin {
				t.Errorf("origin = %q, want %q", origin, c.wantOrigin)
			}
		})
	}
}

// TestProcessDetailsReportsStatus covers the diagnostic itself. Without
// it, a host where `ps` is absent or refuses looks exactly like one where
// every listener genuinely had nothing extra to show — which is precisely
// how this went undiagnosed the first time: the page simply stayed the
// way it had always looked, with nothing anywhere saying why.
func TestProcessDetailsReportsStatus(t *testing.T) {
	ctx := t.Context()

	t.Run("no pids is a success, not a failure", func(t *testing.T) {
		// A host whose `ss` reported no PIDs at all (run without root, say)
		// gives this step nothing to do — that must not be reported as ps
		// being broken.
		_, status := ProcessDetails(ctx, collect.NewFixtures(t.TempDir()), nil)
		if !status.Available || status.Error != "" {
			t.Errorf("available/error = %v/%q, want true/empty", status.Available, status.Error)
		}
	})

	t.Run("missing ps is reported", func(t *testing.T) {
		// An empty fixtures tree has no ps stub, so the call fails the same
		// way a host without procps would.
		_, status := ProcessDetails(ctx, collect.NewFixtures(t.TempDir()), []int{1})
		if status.Available {
			t.Error("available = true even though ps could not run")
		}
		if status.Error == "" {
			t.Error("error is empty — the failure would be invisible in the Источники table")
		}
	})

	t.Run("working host reports available and resolves details", func(t *testing.T) {
		root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "host"))
		if err != nil {
			t.Fatal(err)
		}
		details, status := ProcessDetails(ctx, collect.NewFixtures(root), []int{812, 1400})
		if !status.Available {
			t.Fatalf("available = false: %s", status.Error)
		}
		if got := details[1400]; got.User != "deploy" || got.Origin != model.OriginManual {
			t.Errorf("pid 1400 = user %q origin %q, want deploy/%s", got.User, got.Origin, model.OriginManual)
		}
		if got := details[812]; got.Unit != "nginx.service" || got.Origin != model.OriginService {
			t.Errorf("pid 812 = unit %q origin %q, want nginx.service/%s", got.Unit, got.Origin, model.OriginService)
		}
	})
}

// TestParsePSLine is mostly about the command line surviving intact:
// re-joining split fields would collapse the inner spacing that
// distinguishes one invocation of an interpreter from another, which is
// the single most useful thing this whole lookup produces.
func TestParsePSLine(t *testing.T) {
	t.Run("normal row", func(t *testing.T) {
		pid, d, ok := parsePSLine("  812 root      1234567 nginx: master process /usr/sbin/nginx -g daemon on;")
		if !ok {
			t.Fatal("parsePSLine returned ok = false")
		}
		if pid != 812 {
			t.Errorf("pid = %d, want 812", pid)
		}
		if d.User != "root" {
			t.Errorf("user = %q, want root", d.User)
		}
		if d.UptimeS != 1234567 {
			t.Errorf("uptime = %d, want 1234567", d.UptimeS)
		}
		if want := "nginx: master process /usr/sbin/nginx -g daemon on;"; d.Command != want {
			t.Errorf("command = %q, want %q", d.Command, want)
		}
	})

	t.Run("command with repeated inner spaces is preserved verbatim", func(t *testing.T) {
		_, d, ok := parsePSLine("1400 alex 42 python3 /opt/x.py --flag  'a  b'")
		if !ok {
			t.Fatal("parsePSLine returned ok = false")
		}
		if want := "python3 /opt/x.py --flag  'a  b'"; d.Command != want {
			t.Errorf("command = %q, want %q", d.Command, want)
		}
	})

	t.Run("rejects rows that are not process lines", func(t *testing.T) {
		for _, line := range []string{"", "   ", "no numbers here at all", "812 root 5"} {
			if _, _, ok := parsePSLine(line); ok {
				t.Errorf("parsePSLine(%q) = ok, want rejected", line)
			}
		}
	})
}
