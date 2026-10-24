package deploy

import (
	"strings"
	"testing"
)

// Потолки токена в compose: сервис без limits получает потолок, сервис с
// limits выше потолка — отказ, replicas умножают; выход за стек — отказ.
func TestApplyTokenLimits(t *testing.T) {
	text := `services:
  web:
    image: nginx:1.27
    volumes:
      - ./data:/data
      - cache:/cache
  api:
    image: app:1
    deploy:
      replicas: 2
      resources:
        limits:
          cpus: "0.5"
          memory: 256M
volumes:
  cache: {}
`
	out, need, err := ApplyTokenLimits(text, ServiceCaps{CPU: 1, MemMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	if need.CPU != 2 || need.MemMB != 1024 || len(need.Unlimited) != 0 {
		t.Fatalf("need: %+v", need)
	}
	if !strings.Contains(out, `cpus: "1"`) || !strings.Contains(out, `memory: "512M"`) {
		t.Fatalf("caps not injected:\n%s", out)
	}
	if _, _, err := ApplyTokenLimits(strings.Replace(text, `cpus: "0.5"`, `cpus: "2"`, 1), ServiceCaps{CPU: 1, MemMB: 512}); err == nil || !strings.Contains(err.Error(), "api") {
		t.Fatalf("over cap: %v", err)
	}
	if _, _, err := ApplyTokenLimits(strings.Replace(text, "memory: 256M", "memory: 1g", 1), ServiceCaps{CPU: 1, MemMB: 512}); err == nil {
		t.Fatal("over memory cap accepted")
	}
	for _, bad := range []string{
		"services:\n  x:\n    image: a\n    privileged: true\n",
		"services:\n  x:\n    image: a\n    network_mode: host\n",
		"services:\n  x:\n    image: a\n    cap_add: [NET_ADMIN]\n",
		"services:\n  x:\n    image: a\n    volumes: [\"/etc:/host-etc\"]\n",
		"services:\n  x:\n    image: a\n    volumes: [\"/var/run/docker.sock:/var/run/docker.sock\"]\n",
		"services:\n  x:\n    image: a\n    volumes: [\"../other:/o\"]\n",
		"services:\n  x:\n    image: a\n    volumes:\n      - type: bind\n        source: /srv\n        target: /s\n",
		"services:\n  x:\n    image: a\n    devices: [\"/dev/sda:/dev/sda\"]\n",
	} {
		if _, _, err := ApplyTokenLimits(bad, ServiceCaps{CPU: 1, MemMB: 512}); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
	// Без потолка сервиса — сервис без limits не посчитать.
	if _, need, err := ApplyTokenLimits("services:\n  x:\n    image: a\n", ServiceCaps{}); err != nil || len(need.Unlimited) != 1 {
		t.Fatalf("unlimited: %+v %v", need, err)
	}
	for in, want := range map[string]int64{"512M": 512, "1g": 1024, "1.5GiB": 1536, "1048576": 1, "64mb": 64} {
		if got, ok := parseMemMB(in); !ok || got != want {
			t.Errorf("%s: %d %v", in, got, ok)
		}
	}
}
