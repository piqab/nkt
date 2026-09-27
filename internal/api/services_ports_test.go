package api

import (
	"testing"

	"github.com/piqab/nkt/internal/model"
)

// Порт службы находится по её юниту (рабочие процессы nginx) и по
// главному PID; чужие сокеты и повторы не попадают.
func TestServicesWithPorts(t *testing.T) {
	services := []model.ServiceUnit{{Name: "nginx", Unit: "nginx.service", MainPID: 10}, {Name: "redis", Unit: "redis-server.service", MainPID: 20}, {Name: "firewalld", Unit: "firewalld", MainPID: 11}}
	listeners := []model.Listener{
		{Protocol: "tcp", Address: "0.0.0.0", Port: 443, PID: 11, Unit: "nginx.service"},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 443, PID: 12, Unit: "nginx.service"},
		{Protocol: "tcp", Address: "::", Port: 80, PID: 10},
		{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, PID: 20},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 22, PID: 30, Unit: "ssh.service"},
	}
	got := servicesWithPorts(services, listeners)
	if len(got[0].Ports) != 2 || got[0].Ports[0] != "tcp 0.0.0.0:443" || got[0].Ports[1] != "tcp [::]:80" {
		t.Errorf("nginx: %v", got[0].Ports)
	}
	if len(got[1].Ports) != 1 || got[1].Ports[0] != "tcp 127.0.0.1:6379" {
		t.Errorf("redis: %v", got[1].Ports)
	}
	// PID совпал с процессом nginx, но у сокета юнит nginx — не firewalld.
	if len(got[2].Ports) != 0 {
		t.Errorf("firewalld: %v", got[2].Ports)
	}
}
