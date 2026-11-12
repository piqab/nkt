package k8s

import (
	"strings"
	"testing"
)

func TestLBInstallSpecValidate(t *testing.T) {
	tests := []struct {
		name    string
		spec    LBInstallSpec
		wantErr bool
	}{
		{
			name: "valid full spec",
			spec: LBInstallSpec{
				Endpoint: "10.77.1.10:6443",
				CPAddrs:  []string{"10.77.1.2:6443", "10.77.1.6:6443", "10.77.1.7:6443"},
				VIP:      "10.77.1.10",
				IsMaster: true,
				Haproxy:  LBHaproxyConfig{ConnectTimeout: 5000, ClientTimeout: 50000, ServerTimeout: 50000, MaxConn: 2000, StatsPort: 8404, Algorithm: "source"},
			},
			wantErr: false,
		},
		{
			name: "missing endpoint",
			spec: LBInstallSpec{
				CPAddrs: []string{"10.77.1.2:6443"},
			},
			wantErr: true,
		},
		{
			name: "missing cp addrs",
			spec: LBInstallSpec{
				Endpoint: "10.77.1.10:6443",
			},
			wantErr: true,
		},
		{
			name: "defaults applied",
			spec: LBInstallSpec{
				Endpoint: "10.77.1.10:6443",
				CPAddrs:  []string{"10.77.1.2:6443"},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			// Проверка применения умолчаний.
			if !tt.wantErr {
				if tt.spec.Haproxy.ConnectTimeout != 5000 {
					t.Errorf("ConnectTimeout = %d, want 5000", tt.spec.Haproxy.ConnectTimeout)
				}
				if tt.spec.Haproxy.ClientTimeout != 50000 {
					t.Errorf("ClientTimeout = %d, want 50000", tt.spec.Haproxy.ClientTimeout)
				}
				if tt.spec.Haproxy.ServerTimeout != 50000 {
					t.Errorf("ServerTimeout = %d, want 50000", tt.spec.Haproxy.ServerTimeout)
				}
				if tt.spec.Haproxy.MaxConn != 2000 {
					t.Errorf("MaxConn = %d, want 2000", tt.spec.Haproxy.MaxConn)
				}
				if tt.spec.Haproxy.Algorithm != "source" {
					t.Errorf("Algorithm = %q, want source", tt.spec.Haproxy.Algorithm)
				}
			}
		})
	}
}

func TestLBSteps(t *testing.T) {
	spec := LBInstallSpec{
		Endpoint: "10.77.1.10:6443",
		CPAddrs:  []string{"10.77.1.2:6443", "10.77.1.6:6443", "10.77.1.7:6443"},
		VIP:      "10.77.1.10",
		IsMaster: true,
		Haproxy:  LBHaproxyConfig{StatsPort: 8404},
	}
	steps := LBSteps(spec)
	if len(steps) != 4 {
		t.Fatalf("LBSteps() returned %d steps, want 4", len(steps))
	}
	// Шаг 1: пакеты.
	if !strings.Contains(steps[0].Script, "apt-get") {
		t.Errorf("step 0 script missing apt-get: %s", steps[0].Script)
	}
	// Шаг 2: HAProxy конфиг.
	if !strings.Contains(steps[1].Script, "haproxy.cfg") {
		t.Errorf("step 1 script missing haproxy.cfg: %s", steps[1].Script)
	}
	if !strings.Contains(steps[1].Script, "10.77.1.2:6443") {
		t.Errorf("step 1 script missing cp-1 addr: %s", steps[1].Script)
	}
	// Шаг 3: keepalived конфиг (есть VIP).
	if !strings.Contains(steps[2].Script, "keepalived.conf") {
		t.Errorf("step 2 script missing keepalived.conf: %s", steps[2].Script)
	}
	if !strings.Contains(steps[2].Script, "MASTER") {
		t.Errorf("step 2 script missing MASTER state: %s", steps[2].Script)
	}
	// Шаг 4: запуск служб.
	if !strings.Contains(steps[3].Script, "systemctl") {
		t.Errorf("step 3 script missing systemctl: %s", steps[3].Script)
	}
	if !strings.Contains(steps[3].Script, "keepalived") {
		t.Errorf("step 3 script missing keepalived restart: %s", steps[3].Script)
	}
}

func TestLBStepsNoVIP(t *testing.T) {
	spec := LBInstallSpec{
		Endpoint: "10.77.1.2:6443",
		CPAddrs:  []string{"10.77.1.2:6443"},
		// VIP не задан — один LB, без keepalived.
	}
	steps := LBSteps(spec)
	if len(steps) != 3 {
		t.Fatalf("LBSteps() without VIP returned %d steps, want 3", len(steps))
	}
	// Нет шага keepalived.
	for _, st := range steps {
		if strings.Contains(st.Title, "keepalived") && strings.Contains(st.Title, "Config") {
			t.Errorf("unexpected keepalived config step: %s", st.Title)
		}
	}
	// В шаге запуска нет restart keepalived (но enable есть — пакет установлен).
	if strings.Contains(steps[len(steps)-1].Script, "restart keepalived") {
		t.Errorf("last step should not restart keepalived without VIP: %s", steps[len(steps)-1].Script)
	}
}

func TestMasterState(t *testing.T) {
	if got := masterState(true); got != "MASTER" {
		t.Errorf("masterState(true) = %q, want MASTER", got)
	}
	if got := masterState(false); got != "BACKUP" {
		t.Errorf("masterState(false) = %q, want BACKUP", got)
	}
}

// VRRP unicast'ом при заданном соседе: мультикаст-анонсы машины в macvtap
// на одном хосте друг другу не доставляют и держали бы VIP вдвоём.
func TestKeepalivedUnicastPeer(t *testing.T) {
	spec := LBInstallSpec{
		Endpoint: "10.77.1.10:6443",
		CPAddrs:  []string{"10.77.1.2:6443"},
		VIP:      "10.77.1.10",
		IsMaster: true,
		PeerAddr: "10.77.1.6",
	}
	s := lbKeepalivedConfigScript(spec)
	for _, want := range []string{
		"unicast_src_ip $OWNIP",
		"unicast_peer {\n        10.77.1.6\n    }",
		"OWNIP=$(ip -4 -o addr show dev \"$IFACE\"",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("keepalived config lacks %q:\n%s", want, s)
		}
	}
	// Скрипт должен остаться валидным shell'ом: heredoc не поломан вставкой.
	checkBashSyntax(t, "keepalived config with unicast", s)

	// Без соседа (одиночный LB без HA или старый хаб) — прежний мультикаст.
	alone := lbKeepalivedConfigScript(LBInstallSpec{
		Endpoint: "10.77.1.10:6443", CPAddrs: []string{"10.77.1.2:6443"}, VIP: "10.77.1.10",
	})
	if strings.Contains(alone, "unicast") {
		t.Errorf("без соседа unicast быть не должен:\n%s", alone)
	}
	checkBashSyntax(t, "keepalived config without unicast", alone)

	// Адрес соседа проверяется до установки, а не в конфиге keepalived.
	bad := LBInstallSpec{Endpoint: "e:6443", CPAddrs: []string{"c:6443"}, PeerAddr: "не-адрес"}
	if err := bad.Validate(); err == nil {
		t.Error("кривой адрес соседа принят")
	}
}
