package vmcreate

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Свой user-data: только #cloud-config без ключей nkt; списки
// складываются с пакетами nkt; учётная запись и ключ хаба остаются.
func TestUserDataMerge(t *testing.T) {
	for text, bad := range map[string]bool{
		"#cloud-config\nruncmd: [\"echo hi\"]\n":        false,
		"#!/bin/sh\necho hi\n":                          true,
		"#include\nhttps://example.com/x\n":             true,
		"#cloud-config\nusers: []\n":                    true,
		"#cloud-config\nssh_authorized_keys: [\"x\"]\n": true,
		"#cloud-config\n- a\n":                          true,
		"#cloud-config\n" + strings.Repeat("#", 70<<10): true,
	} {
		if _, err := CheckUserData(text); (err != nil) != bad {
			t.Errorf("%.40q: err=%v", text, err)
		}
	}
	s := createSpec()
	s.Packages = []string{"nginx"}
	s.ExtraKeys = []string{"ssh-ed25519 AAAAHUB hub"}
	s.UserData = "#cloud-config\npackages: [htop]\nruncmd:\n  - touch /ok\nwrite_files:\n  - path: /etc/motd\n    content: hi\n"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	out := UserData(s)
	if !strings.HasPrefix(out, "#cloud-config\n") {
		t.Fatalf("header: %s", out)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	pk, _ := doc["packages"].([]any)
	if len(pk) != 2 || pk[0] != "nginx" || pk[1] != "htop" || doc["runcmd"] == nil || doc["write_files"] == nil {
		t.Fatalf("merge: %v", doc)
	}
	if !strings.Contains(out, "AAAAHUB") || !strings.Contains(out, "deploy") {
		t.Fatalf("hub access lost:\n%s", out)
	}
	s.UserData = "#cloud-config\nusers: []\n"
	if s.Validate() == nil {
		t.Fatal("reserved key accepted")
	}
}

// paths пишет write_files merged-настроки как список путей: так видно,
// что свой user-data не перезаписан и что скрипт агента на месте.
func paths(t *testing.T, out string) []string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("yaml: %v\n%s", err, out)
	}
	list, _ := doc["write_files"].([]any)
	var got []string
	for _, item := range list {
		entry, _ := item.(map[string]any)
		p, _ := entry["path"].(string)
		got = append(got, p)
	}
	return got
}

// Гостевой агент: пакет сам службу не поднимает, поэтому скрипт
// per-instance заводится только когда агент запрошен, лежит в каталоге
// первого запуска и не затирает файлы владельца машины.
func TestGuestAgentStartupScript(t *testing.T) {
	s := createSpec()
	s.Packages = []string{"qemu-guest-agent"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	out := UserData(s)
	if !slices.Contains(paths(t, out), guestAgentScript) {
		t.Fatalf("агент запрошен, скрипта нет:\n%s", out)
	}
	if !strings.Contains(out, "systemctl enable --now qemu-guest-agent") ||
		!strings.Contains(out, "permissions: '0755'") {
		t.Fatalf("скрипт не запускает службу:\n%s", out)
	}
	// Скрипт per-instance выполняется один на instance-id: на второй
	// загрузке он уже не нужен.
	if !strings.Contains(guestAgentScript, "/scripts/per-instance/") {
		t.Fatalf("не тот каталог: %s", guestAgentScript)
	}

	// Без запроса агента его быть не должно: у машины без него свои файлы.
	plain := createSpec()
	plain.Packages = []string{"nginx"}
	if got := paths(t, UserData(plain)); len(got) != 0 {
		t.Fatalf("write_files от nkt без запроса: %v", got)
	}

	// Свой user-data с файлами складывается, а не заменяется.
	own := createSpec()
	own.Packages = []string{"qemu-guest-agent"}
	own.UserData = "#cloud-config\nwrite_files:\n  - path: /etc/motd\n    content: hi\n"
	if err := own.Validate(); err != nil {
		t.Fatal(err)
	}
	got := paths(t, UserData(own))
	if !slices.Contains(got, guestAgentScript) || !slices.Contains(got, "/etc/motd") {
		t.Fatalf("файлы потеряны: %v", got)
	}
}
