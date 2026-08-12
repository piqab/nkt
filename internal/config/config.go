// Package config holds every setting that differs between a developer laptop
// (fixtures mode) and a real Linux host (local mode), so the rest of the code
// never has to branch on the operating system.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Mode selects where host data comes from.
type Mode string

const (
	// ModeFixtures reads a canned host snapshot from disk. Works on Windows.
	ModeFixtures Mode = "fixtures"
	// ModeLocal reads the real filesystem and runs real commands. Linux only.
	ModeLocal Mode = "local"
	// ModeHub runs as a control center for other nkt instances over SSH
	// instead of inspecting any local host — see internal/hub.
	ModeHub Mode = "hub"
)

// Config is the fully resolved application configuration.
type Config struct {
	Mode         Mode
	FixturesRoot string
	DataDir      string

	// Inspected locations, expressed as paths on the target host.
	NginxRoot        string
	NginxMainConfig  string
	HAProxyRoot      string
	HAProxyMainConf  string
	ComposeFiles     []string
	NginxAccessLogs  []string
	HAProxyAccessLog []string
	DockerSocket     string
	PodmanSocket     string
	LibvirtURI       string

	// Monitoring cadence.
	ProbeInterval     time.Duration
	ProbeTimeout      time.Duration
	MetricsInterval   time.Duration
	LogScanInterval   time.Duration
	InventoryInterval time.Duration
	Retention         time.Duration
	SchedulerEnabled  bool
	// DemoBackfill seeds synthetic history in fixtures mode so the availability
	// and usage schedules have something to show on a fresh database.
	DemoBackfill bool

	// Control plane.
	AllowMutations bool
	CommandTimeout time.Duration
	// CertbotTimeout overrides CommandTimeout specifically for `certbot
	// renew` — an ACME challenge round-trip to Let's Encrypt routinely takes
	// longer than the couple of seconds every other host command needs, and
	// forcing it into the same short ceiling kills the process mid-renewal.
	CertbotTimeout time.Duration
	// CertbotEmail is passed to `certbot certonly` as --email when issuing a
	// brand-new certificate, so Let's Encrypt can warn about that specific
	// certificate's own expiry — separate from and in addition to whatever
	// this app's own renewal/expiry monitoring already does. Left empty,
	// certonly runs with --register-unsafely-without-email instead.
	CertbotEmail string

	// AutoRenewCerts periodically runs `certbot renew --cert-name <lineage>`
	// for every certbot-managed certificate the analyzer already flags as
	// expired or expiring — a fallback for hosts where certbot.timer or the
	// cron job never got enabled. Off by default: an unattended process that
	// reaches out to Let's Encrypt on its own is a deliberate opt-in, and it
	// still respects AllowMutations.
	AutoRenewCerts         bool
	AutoRenewCertsInterval time.Duration
	// AutoRenewCertsWithin is how close to expiry (or already expired) a
	// certificate must be before the job touches it — the same margin
	// Let's Encrypt itself uses to decide a renewal is actually due.
	AutoRenewCertsWithin time.Duration

	// Auth.
	SessionTTL             time.Duration
	BootstrapAdminUser     string
	BootstrapAdminPassword string
	CookieSecure           bool

	// HTTP.
	Addr        string
	CORSOrigins []string
	DevProxyUI  bool

	// Hub only (ModeHub): base64-encoded AES-256 key used to encrypt SSH
	// credentials and remote session tokens at rest. Left empty, the hub
	// generates one on first start and persists it under DataDir instead —
	// see secretbox.ResolveKey.
	HubMasterKey string
	// HubSourceRoot is where the hub cross-compiles nkt for a remote host's
	// architecture (`go build` is run with this as its working directory) —
	// the hub image bakes the module source in here at build time.
	HubSourceRoot string
	// HubGoBin is the `go` binary the hub invokes to cross-compile nkt for
	// each new host's architecture. Defaults to "go", resolved via PATH —
	// which works inside the Docker image (golang:1.26-alpine) but not
	// necessarily under systemd: a `go` installed by `make native-build`
	// into $HOME/.local/go/bin is invisible to a service, whose PATH is
	// never the interactive shell's. Set this to an absolute path in that
	// case instead of relying on PATH.
	HubGoBin string
}

// defaultMode picks the mode for a bare invocation. Linux is the platform this
// application manages, so there a plain `nkt` inspects the real host; anywhere
// else local mode cannot work at all and the snapshot is the only useful choice.
func defaultMode() Mode {
	if runtime.GOOS == "linux" {
		return ModeLocal
	}
	return ModeFixtures
}

// defaultDataDir keeps production state in the standard system location, which
// is also where the systemd unit points. That matters beyond tidiness: the
// terminal interface reads the same database the service writes, and a
// directory next to the binary would leave it staring at an empty history.
func defaultDataDir(mode Mode, wd string) string {
	if mode == ModeLocal || mode == ModeHub {
		return "/var/lib/netknownsthat"
	}
	return filepath.Join(wd, "data")
}

// Load resolves configuration from NKT_* environment variables, falling back to
// defaults that are sensible for a Debian/Ubuntu host.
func Load() (*Config, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}

	mode := Mode(envStr("NKT_MODE", string(defaultMode())))

	c := &Config{
		Mode:         mode,
		FixturesRoot: envStr("NKT_FIXTURES_ROOT", filepath.Join(wd, "fixtures", "host")),
		DataDir:      envStr("NKT_DATA_DIR", defaultDataDir(mode, wd)),

		NginxRoot:        envStr("NKT_NGINX_ROOT", "/etc/nginx"),
		NginxMainConfig:  envStr("NKT_NGINX_MAIN_CONFIG", "/etc/nginx/nginx.conf"),
		HAProxyRoot:      envStr("NKT_HAPROXY_ROOT", "/etc/haproxy"),
		HAProxyMainConf:  envStr("NKT_HAPROXY_MAIN_CONFIG", "/etc/haproxy/haproxy.cfg"),
		ComposeFiles:     envList("NKT_COMPOSE_FILES", "/srv/docker/docker-compose.yml,/opt/stacks/docker-compose.yml"),
		NginxAccessLogs:  envList("NKT_NGINX_ACCESS_LOGS", "/var/log/nginx/access.log"),
		HAProxyAccessLog: envList("NKT_HAPROXY_ACCESS_LOGS", "/var/log/haproxy.log"),
		DockerSocket:     envStr("NKT_DOCKER_SOCKET", "/var/run/docker.sock"),
		PodmanSocket:     envStr("NKT_PODMAN_SOCKET", "/run/podman/podman.sock"),
		LibvirtURI:       envStr("NKT_LIBVIRT_URI", "qemu:///system"),

		ProbeInterval:     envDur("NKT_PROBE_INTERVAL", time.Minute),
		ProbeTimeout:      envDur("NKT_PROBE_TIMEOUT", 5*time.Second),
		MetricsInterval:   envDur("NKT_METRICS_INTERVAL", time.Minute),
		LogScanInterval:   envDur("NKT_LOG_SCAN_INTERVAL", 5*time.Minute),
		InventoryInterval: envDur("NKT_INVENTORY_INTERVAL", 5*time.Minute),
		Retention:         envDur("NKT_RETENTION", 30*24*time.Hour),
		SchedulerEnabled:  envBool("NKT_SCHEDULER_ENABLED", true),
		DemoBackfill:      envBool("NKT_DEMO_BACKFILL", true),

		AllowMutations: envBool("NKT_ALLOW_MUTATIONS", true),
		CommandTimeout: envDur("NKT_COMMAND_TIMEOUT", 30*time.Second),
		CertbotTimeout: envDur("NKT_CERTBOT_TIMEOUT", 3*time.Minute),
		CertbotEmail:   envStr("NKT_CERTBOT_EMAIL", ""),

		AutoRenewCerts:         envBool("NKT_AUTO_RENEW_CERTS", false),
		AutoRenewCertsInterval: envDur("NKT_AUTO_RENEW_INTERVAL", 6*time.Hour),
		AutoRenewCertsWithin:   envDur("NKT_AUTO_RENEW_WITHIN", 30*24*time.Hour),

		SessionTTL:             envDur("NKT_SESSION_TTL", 12*time.Hour),
		BootstrapAdminUser:     envStr("NKT_BOOTSTRAP_ADMIN_USER", "admin"),
		BootstrapAdminPassword: envStr("NKT_BOOTSTRAP_ADMIN_PASSWORD", ""),
		CookieSecure:           envBool("NKT_COOKIE_SECURE", true),

		Addr:        envStr("NKT_ADDR", "127.0.0.1:8077"),
		CORSOrigins: envList("NKT_CORS_ORIGINS", "http://localhost:5173"),
		DevProxyUI:  envBool("NKT_DEV_PROXY_UI", false),

		HubMasterKey:  envStr("NKT_HUB_MASTER_KEY", ""),
		HubSourceRoot: envStr("NKT_HUB_SOURCE_ROOT", wd),
		HubGoBin:      envStr("NKT_HUB_GO_BIN", "go"),
	}

	switch c.Mode {
	case ModeFixtures, ModeLocal, ModeHub:
	default:
		return nil, fmt.Errorf("NKT_MODE must be %q, %q or %q, got %q", ModeFixtures, ModeLocal, ModeHub, c.Mode)
	}

	if err := os.MkdirAll(c.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf(
			"не удалось создать каталог данных %s: %w. Запустите от root или задайте NKT_DATA_DIR",
			c.DataDir, err)
	}
	return c, nil
}

// DBPath is the SQLite database file.
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "netknownsthat.db") }

// HistoryDir stores every observed and edited version of every managed config.
func (c *Config) HistoryDir() string { return filepath.Join(c.DataDir, "config-history") }

// HubKeyFile is where the hub's secretbox master key is persisted when
// NKT_HUB_MASTER_KEY is not set.
func (c *Config) HubKeyFile() string { return filepath.Join(c.DataDir, "hub.key") }

// HubBinCacheDir caches nkt binaries the hub has already cross-compiled for
// a given remote architecture, so installing a second host of the same
// arch/version skips the build step.
func (c *Config) HubBinCacheDir() string { return filepath.Join(c.DataDir, "bin-cache") }

// IsFixtures reports whether the app runs against a canned snapshot.
func (c *Config) IsFixtures() bool { return c.Mode == ModeFixtures }

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envList(key, def string) []string {
	raw := envStr(key, def)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envDur(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	// Bare numbers are read as seconds, which is friendlier in a systemd unit.
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
