package control

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// Проверка имён перед certbot: своё имя проходит, чужое отвечающее —
// с предупреждением, неотвечающее и нерезолвящееся — отказ.
func TestVerifyDomains(t *testing.T) {
	lookup := func(_ context.Context, name string) ([]net.IPAddr, error) {
		switch name {
		case "here.example":
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.5")}}, nil
		case "proxy.example":
			return []net.IPAddr{{IP: net.ParseIP("198.51.100.9")}}, nil
		case "dead.example":
			return []net.IPAddr{{IP: net.ParseIP("192.0.2.77")}}, nil
		}
		return nil, errors.New("no such host")
	}
	ping := func(ip string) bool { return ip == "198.51.100.9" }
	local := map[string]bool{"203.0.113.5": true, "127.0.0.1": true}
	var log []string
	report := &certProgress{msg: func(key string, args ...any) { log = append(log, key) }}

	if err := verifyDomains(context.Background(), []string{"here.example", "proxy.example"}, local, lookup, ping, report); err != nil {
		t.Fatalf("свои и отвечающие имена: %v", err)
	}
	if strings.Join(log, ",") != "certgen.domainPointsHere,certgen.domainElsewhere" {
		t.Errorf("журнал: %v", log)
	}
	if err := verifyDomains(context.Background(), []string{"dead.example"}, local, lookup, ping, report); err == nil || !strings.Contains(err.Error(), "192.0.2.77") {
		t.Errorf("неотвечающий адрес должен быть отказом: %v", err)
	}
	if err := verifyDomains(context.Background(), []string{"nowhere.example"}, local, lookup, ping, report); err == nil || !strings.Contains(err.Error(), "nowhere.example") {
		t.Errorf("нерезолвящееся имя должно быть отказом: %v", err)
	}
}
