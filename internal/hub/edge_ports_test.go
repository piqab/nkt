package hub

import "testing"

// Имена процессов из вывода ss -ltnp: по ним задание установки edge
// называет, кто держит порт.
func TestSSProcessNames(t *testing.T) {
	out := `LISTEN 0 511 0.0.0.0:443 0.0.0.0:* users:(("nginx",pid=812,fd=8),("nginx",pid=813,fd=8))`
	m := ssProcRe.FindAllStringSubmatch(out, -1)
	if len(m) != 2 || m[0][1] != "nginx" {
		t.Fatalf("%v", m)
	}
}
