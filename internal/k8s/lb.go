package k8s

import (
	"context"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// KindLBInstall — задание хоста: поставить балансировщик (HAProxy +
// keepalived) на машине кластера.
const KindLBInstall = "k8s.lb-install"

// LBInstallSpec — параметры установки балансировщика.
type LBInstallSpec struct {
	// Endpoint — адрес API кластера (VIP:6443 или lb-1:6443).
	Endpoint string `json:"endpoint"`
	// CPAddrs — адреса control plane узлов (host:6443).
	CPAddrs []string `json:"cp_addrs"`
	// VIP — виртуальный адрес HA-пары (keepalived). Пусто — без HA.
	VIP string `json:"vip,omitempty"`
	// IsMaster — эта машина — master в HA-паре (приоритет keepalived выше).
	IsMaster bool `json:"is_master,omitempty"`
	// Haproxy — параметры HAProxy.
	Haproxy LBHaproxyConfig `json:"haproxy"`
}

// LBHaproxyConfig — параметры HAProxy для балансировщика.
type LBHaproxyConfig struct {
	ConnectTimeout int    `json:"connect_timeout,omitempty"`
	ClientTimeout  int    `json:"client_timeout,omitempty"`
	ServerTimeout  int    `json:"server_timeout,omitempty"`
	MaxConn        int    `json:"max_conn,omitempty"`
	StatsPort      int    `json:"stats_port,omitempty"`
	Algorithm      string `json:"algorithm,omitempty"`
}

// Validate проверяет форму LBInstallSpec.
func (s *LBInstallSpec) Validate() error {
	if s.Endpoint == "" {
		return msgs.Errorf("k8s.lbEndpointRequired")
	}
	if len(s.CPAddrs) == 0 {
		return msgs.Errorf("k8s.lbCPAddrsRequired")
	}
	if s.Haproxy.ConnectTimeout <= 0 {
		s.Haproxy.ConnectTimeout = 5000
	}
	if s.Haproxy.ClientTimeout <= 0 {
		s.Haproxy.ClientTimeout = 50000
	}
	if s.Haproxy.ServerTimeout <= 0 {
		s.Haproxy.ServerTimeout = 50000
	}
	if s.Haproxy.MaxConn <= 0 {
		s.Haproxy.MaxConn = 2000
	}
	if s.Haproxy.Algorithm == "" {
		s.Haproxy.Algorithm = "source-hash"
	}
	return nil
}

// LBInstallRunner — задание хоста: установка балансировщика.
type LBInstallRunner struct{ m *Manager }

func NewLBInstallRunner(m *Manager) *LBInstallRunner { return &LBInstallRunner{m: m} }

// Resumable — да: сделанные шаги записаны.
func (r *LBInstallRunner) Resumable() bool { return true }

// Run выполняет шаги установки балансировщика.
func (r *LBInstallRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var spec LBInstallSpec
	if err := jc.Params(&spec); err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if r.m.run == nil {
		return msgs.Errorf("control.installationUnavailableMode")
	}
	var done struct {
		Steps int `json:"steps"`
	}
	_ = jc.LoadResume(&done)
	steps := LBSteps(spec)
	for i, st := range steps {
		jc.StepKey(i+1, len(steps), st.Title)
		if i < done.Steps {
			jc.Log("k8s.stepSkipped")
			continue
		}
		out, err := r.m.run(ctx, "sh", "-c", st.Script)
		if err != nil {
			return msgs.Errorf("k8s.stepFailed", msgs.T(jc.Lang(), st.Title), err)
		}
		for _, line := range tail(out.Stdout, 8) {
			jc.Logf("      %s", line)
		}
		if out.ExitCode != 0 {
			for _, line := range tail(out.Stderr, 12) {
				jc.Logf("      ! %s", line)
			}
			return msgs.Errorf("k8s.stepFailed", msgs.T(jc.Lang(), st.Title), fmt.Sprintf("exit %d: %s", out.ExitCode, failureLine(out)))
		}
		done.Steps = i + 1
		jc.SaveResume(done)
	}
	jc.Log("k8s.lbInstalled", spec.Endpoint)
	return nil
}

// LBStep — один шаг установки балансировщика: заголовок (ключ msgs) и скрипт.
type LBStep struct {
	Title  string
	Script string
}

// LBSteps возвращает шаги установки балансировщика.
func LBSteps(spec LBInstallSpec) []LBStep {
	var steps []LBStep

	// Шаг 1: установка пакетов.
	steps = append(steps, LBStep{
		Title:  "k8s.lbInstallPackages",
		Script: "apt-get update && apt-get install -y haproxy keepalived",
	})

	// Шаг 2: генерация конфига HAProxy.
	steps = append(steps, LBStep{
		Title:  "k8s.lbHaproxyConfig",
		Script: lbHaproxyConfigScript(spec),
	})

	// Шаг 3: генерация конфига keepalived (если HA).
	if spec.VIP != "" {
		steps = append(steps, LBStep{
			Title:  "k8s.lbKeepalivedConfig",
			Script: lbKeepalivedConfigScript(spec),
		})
	}

	// Шаг 4: запуск служб.
	steps = append(steps, LBStep{
		Title:  "k8s.lbStartServices",
		Script: "systemctl enable haproxy keepalived && systemctl restart haproxy" + keepalivedRestart(spec),
	})

	return steps
}

func keepalivedRestart(spec LBInstallSpec) string {
	if spec.VIP == "" {
		return ""
	}
	return " && systemctl restart keepalived"
}

func lbHaproxyConfigScript(spec LBInstallSpec) string {
	backends := ""
	for i, addr := range spec.CPAddrs {
		backends += fmt.Sprintf("    server cp-%d %s check inter 3s fall 3 rise 2\n", i+1, addr)
	}
	statsLine := ""
	if spec.Haproxy.StatsPort > 0 {
		statsLine = fmt.Sprintf(`
listen stats
    bind *:%d
    stats enable
    stats uri /
    stats refresh 5s`, spec.Haproxy.StatsPort)
	}
	return fmt.Sprintf(`cat > /etc/haproxy/haproxy.cfg << 'EOF'
global
    log /dev/log local0
    log /dev/log local1 notice
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin expose-fd listeners
    stats timeout 30s
    user haproxy
    group haproxy
    daemon
    maxconn %d

defaults
    log     global
    mode    tcp
    option  tcplog
    option  dontlognull
    timeout connect %dms
    timeout client  %dms
    timeout server  %dms
    errorfile 400 /etc/haproxy/errors/400.http
    errorfile 403 /etc/haproxy/errors/403.http
    errorfile 408 /etc/haproxy/errors/408.http
    errorfile 500 /etc/haproxy/errors/500.http
    errorfile 502 /etc/haproxy/errors/502.http
    errorfile 503 /etc/haproxy/errors/503.http
    errorfile 504 /etc/haproxy/errors/504.http

frontend k8s_api
    bind *:6443
    default_backend k8s_cp

backend k8s_cp
    balance %s
%s%s
EOF`, spec.Haproxy.MaxConn, spec.Haproxy.ConnectTimeout, spec.Haproxy.ClientTimeout, spec.Haproxy.ServerTimeout, spec.Haproxy.Algorithm, backends, statsLine)
}

func lbKeepalivedConfigScript(spec LBInstallSpec) string {
	priority := 100
	if !spec.IsMaster {
		priority = 90
	}
	return fmt.Sprintf(`IFACE=$(ip -o link show | awk -F': ' '{print $2}' | grep -v lo | head -1)
cat > /etc/keepalived/keepalived.conf << EOF
vrrp_script check_haproxy {
    script "killall -0 haproxy"
    interval 2
    weight 2
}

vrrp_instance VI_1 {
    state %s
    interface $IFACE
    virtual_router_id 51
    priority %d
    advert_int 1
    authentication {
        auth_type PASS
        auth_pass k8s_lb
    }
    virtual_ipaddress {
        %s
    }
    track_script {
        check_haproxy
    }
}
EOF`, masterState(spec.IsMaster), priority, spec.VIP)
}

func masterState(isMaster bool) string {
	if isMaster {
		return "MASTER"
	}
	return "BACKUP"
}

// LBStepTitle возвращает локализованный заголовок шага.
func LBStepTitle(key string) string {
	return msgs.T("ru", key)
}

// lbStepTitles — заголовки шагов LB (не используются напрямую, но нужны для
// полного списка ключей msgs).
var lbStepTitles = []string{
	"k8s.lbInstallPackages",
	"k8s.lbHaproxyConfig",
	"k8s.lbKeepalivedConfig",
	"k8s.lbStartServices",
}

// Ensure imports are used.
var _ = strings.TrimSpace
