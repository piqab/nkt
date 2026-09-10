package control

import "testing"

// Строки взяты из настоящего /etc/passwd Debian 13: список должен
// показывать людей, а не полсотни служебных учёток, заведённых пакетами.
func TestParsePasswdLine(t *testing.T) {
	shown := []string{
		"root:x:0:0:root:/root:/bin/bash",
		"alex:x:1000:1000:alex,,,:/home/alex:/bin/bash",
		"deploy:x:1001:1001::/home/deploy:/bin/sh",
	}
	for _, line := range shown {
		if _, ok := parsePasswdLine(line); !ok {
			t.Errorf("учётка человека отброшена: %q", line)
		}
	}

	hidden := []string{
		"daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin",
		"www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin",
		"systemd-network:x:998:998:systemd Network Management:/:/usr/sbin/nologin",
		"nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin",
		"sync:x:4:65534:sync:/bin:/bin/sync",
		// Учётка с uid человека, но без входа — заведена службой.
		"postgres:x:1002:1002::/var/lib/postgresql:/bin/false",
		"",
		"мусор",
	}
	for _, line := range hidden {
		if u, ok := parsePasswdLine(line); ok {
			t.Errorf("служебная учётка попала в список: %q → %+v", line, u)
		}
	}

	u, _ := parsePasswdLine("alex:x:1000:1000:alex,,,:/home/alex:/bin/bash")
	if u.Name != "alex" || u.UID != 1000 || u.Home != "/home/alex" || u.Shell != "/bin/bash" {
		t.Errorf("разбор строки дал %+v", u)
	}
}

// Ключ приходит из формы и уходит в authorized_keys — то есть решает, кто
// сможет войти на хост. Всё, что не является ключом, обязано отбиваться
// здесь, а не оказываться в файле.
func TestParseAuthorizedKey(t *testing.T) {
	key, err := ParseAuthorizedKey("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyBodyHere alex@laptop")
	if err != nil {
		t.Fatalf("настоящий ключ отклонён: %v", err)
	}
	if key.Type != "ssh-ed25519" || key.Comment != "alex@laptop" {
		t.Errorf("разбор дал %+v", key)
	}
	// Тело ключа на экране не нужно целиком — только начало и конец.
	if len(key.Fingerprint) > 20 || key.Fingerprint == "" {
		t.Errorf("отпечаток %q", key.Fingerprint)
	}

	// Ключ без комментария — обычное дело.
	if _, err := ParseAuthorizedKey("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB"); err != nil {
		t.Errorf("ключ без комментария отклонён: %v", err)
	}

	rejected := []string{
		"",
		"не ключ",
		"ssh-ed25519",
		"ssh-ed25519 не-base64!!",
		// Опции меняют смысл записи (command= превращает вход в запуск
		// одной команды, from= ограничивает адреса) — из формы такое не
		// принимается сознательно.
		`command="/bin/sh" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test`,
		"ssh-dss AAAAB3NzaC1kc3MAAACBA",
	}
	for _, line := range rejected {
		if _, err := ParseAuthorizedKey(line); err == nil {
			t.Errorf("принята строка, которая ключом не является: %q", line)
		}
	}
}

func TestShellSingleQuote(t *testing.T) {
	cases := map[string]string{
		"ssh-ed25519 AAAA test": `'ssh-ed25519 AAAA test'`,
		"it's":                  `'it'\''s'`,
		"$(reboot)":             `'$(reboot)'`,
	}
	for in, want := range cases {
		if got := shellSingleQuote(in); got != want {
			t.Errorf("shellSingleQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
