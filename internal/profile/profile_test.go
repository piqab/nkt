package profile

import (
	"strings"
	"testing"
)

func TestParseAndValidate(t *testing.T) {
	raw := `
version: 1
name: web
packages: [nginx, fail2ban]
services:
  nginx:
    enabled: true
    active: true
files:
  - path: /etc/nginx/conf.d/app.conf
    content: |
      server {}
    mode: "0644"
firewall:
  allow:
    - port: 443
    - port: 53
      proto: udp
users:
  - name: deploy
    sudo: true
    keys: ["ssh-ed25519 AAAAKEY deploy@laptop"]
system:
  hostname: web-01
  timezone: Europe/Moscow
`
	p, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Name != "web" || len(p.Packages) != 2 || p.Services["nginx"].Enabled == nil {
		t.Fatalf("разобрано не то: %+v", p)
	}
	// Умолчание протокола проставляется при нормализации, а не в файле:
	// профиль остаётся таким, каким его написали.
	norm := p.Normalize()
	// Порядок — по номеру порта, поэтому 53/udp идёт первым, а умолчание
	// tcp достаётся 443.
	if norm.Firewall.Allow[0].Port != 53 || norm.Firewall.Allow[0].Proto != "udp" {
		t.Errorf("первый порт = %+v", norm.Firewall.Allow[0])
	}
	if norm.Firewall.Allow[1].Proto != "tcp" {
		t.Errorf("протокол по умолчанию = %q", norm.Firewall.Allow[1].Proto)
	}
	// Порядок предсказуемый — иначе выгрузка в git шумит на пустом месте.
	if norm.Packages[0] != "fail2ban" {
		t.Errorf("пакеты не отсортированы: %q", norm.Packages)
	}
}

// Опечатка в имени поля — ошибка, а не тихо забытая настройка: молча
// проигнорированное «servces:» означало бы, что оператор считает службы
// описанными, а их нет.
func TestParseRejectsUnknownField(t *testing.T) {
	_, err := Parse([]byte("name: web\nservces:\n  nginx:\n    active: true\n"))
	if err == nil {
		t.Fatal("неизвестное поле принято")
	}
}

func TestValidateRejectsDangerousValues(t *testing.T) {
	cases := map[string]string{
		"имя пакета с пробелом":    "packages: [\"nginx; rm -rf /\"]\n",
		"относительный путь файла": "files:\n  - path: etc/app.conf\n    content: x\n",
		"путь с ..":                "files:\n  - path: /etc/../root/.ssh/authorized_keys\n    content: x\n",
		"порт вне диапазона":       "firewall:\n  allow:\n    - port: 70000\n",
		"чужой протокол":           "firewall:\n  allow:\n    - port: 80\n      proto: sctp\n",
		"не ключ SSH":              "users:\n  - name: deploy\n    keys: [\"пароль123\"]\n",
		"имя машины с точкой":      "system:\n  hostname: \"web 01\"\n",
		"дубль файла":              "files:\n  - path: /etc/a.conf\n    content: x\n  - path: /etc/a.conf\n    content: y\n",
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: принято без ошибки", name)
		}
	}
}

// Профиль выгружается в YAML и читается обратно тем же: это то, что
// уезжает в git и возвращается оттуда.
func TestMarshalRoundTrip(t *testing.T) {
	p := Profile{
		Version: Version, Name: "web",
		Packages: []string{"nginx"},
		Services: map[string]Service{"nginx": {Active: Bool(true)}},
		Files:    []File{{Path: "/etc/app.conf", Content: "x\n"}},
	}
	raw, err := p.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(Marshal): %v\n%s", err, raw)
	}
	if back.Name != p.Name || back.Services["nginx"].Active == nil || !*back.Services["nginx"].Active {
		t.Errorf("после круга получилось другое: %+v", back)
	}
	if strings.Contains(string(raw), "firewall") {
		t.Errorf("пустые разделы попали в выгрузку:\n%s", raw)
	}
}
