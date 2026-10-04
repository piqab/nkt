package deploy

import "testing"

func TestPortConflicts(t *testing.T) {
	text := `services:
  web:
    ports:
      - "0.0.0.0:8080:8080"
      - "127.0.0.1:8080:8080"
      - "127.0.0.1:9000:9000"
      - "53:53/udp"
  api:
    ports:
      - "192.168.1.5:9000:80"
      - target: 80
        published: "53"
        protocol: tcp
        host_ip: 127.0.0.1
      - "${PORT}:80"
      - "8000-8001:80"
      - "3000"
  db:
    ports:
      - "[::]:5432:5432"
      - "127.0.0.1:5432:5432"
`
	got := PortConflicts(text)
	if len(got) != 2 {
		t.Fatalf("конфликты: %+v", got)
	}
	if got[0].Port != 5432 || got[1].Port != 8080 || got[1].A != "web 0.0.0.0:8080" || got[1].B != "web 127.0.0.1:8080" {
		t.Fatalf("конфликты: %+v", got)
	}
}
