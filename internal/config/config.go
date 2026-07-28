// Package config holds every setting that differs between a developer laptop
// (fixtures mode) and a real Linux host (local mode), so the rest of the code
// never has to branch on the operating system.
package config

import (
	"fmt"
	"os"
	"path/filepath"
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

	// Auth.
	SessionTTL             time.Duration
	BootstrapAdminUser     string
	BootstrapAdminPassword string
	CookieSecure           bool

	// HTTP.
	Addr        string
	CORSOrigins []string
	DevProxyUI  bool
}

// Load resolves configuration from NKT_* environment variables, falling back to
// defaults that are sensible for a Debian/Ubuntu host.
func Load() (*Config, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}

	c := &Config{
		Mode:         Mode(envStr("NKT_MODE", string(ModeFixtures))),
		FixturesRoot: envStr("NKT_FIXTURES_ROOT", filepath.Join(wd, "fixtures", "host")),
		DataDir:      envStr("NKT_DATA_DIR", filepath.Join(wd, "data")),

		NginxRoot:        envStr("NKT_NGINX_ROOT", "/etc/nginx"),
		NginxMainConfig:  envStr("NKT_NGINX_MAIN_CONFIG", "/etc/nginx/nginx.conf"),
		HAProxyRoot:      envStr("NKT_HAPROXY_ROOT", "/etc/haproxy"),
		HAProxyMainConf:  envStr("NKT_HAPROXY_MAIN_CONFIG", "/etc/haproxy/haproxy.cfg"),
		ComposeFiles:     envList("NKT_COMPOSE_FILES", "/srv/docker/docker-compose.yml,/opt/stacks/docker-compose.yml"),
		NginxAccessLogs:  envList("NKT_NGINX_ACCESS_LOGS", "/var/log/nginx/access.log"),
		HAProxyAccessLog: envList("NKT_HAPROXY_ACCESS_LOGS", "/var/log/haproxy.log"),
		DockerSocket:     envStr("NKT_DOCKER_SOCKET", "/var/run/docker.sock"),

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

		SessionTTL:             envDur("NKT_SESSION_TTL", 12*time.Hour),
		BootstrapAdminUser:     envStr("NKT_BOOTSTRAP_ADMIN_USER", "admin"),
		BootstrapAdminPassword: envStr("NKT_BOOTSTRAP_ADMIN_PASSWORD", ""),
		CookieSecure:           envBool("NKT_COOKIE_SECURE", false),

		Addr:        envStr("NKT_ADDR", "127.0.0.1:8077"),
		CORSOrigins: envList("NKT_CORS_ORIGINS", "http://localhost:5173"),
		DevProxyUI:  envBool("NKT_DEV_PROXY_UI", false),
	}

	switch c.Mode {
	case ModeFixtures, ModeLocal:
	default:
		return nil, fmt.Errorf("NKT_MODE must be %q or %q, got %q", ModeFixtures, ModeLocal, c.Mode)
	}

	if err := os.MkdirAll(c.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", c.DataDir, err)
	}
	return c, nil
}

// DBPath is the SQLite database file.
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "netknownsthat.db") }

// HistoryDir stores every observed and edited version of every managed config.
func (c *Config) HistoryDir() string { return filepath.Join(c.DataDir, "config-history") }

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
