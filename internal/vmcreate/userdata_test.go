package vmcreate

import (
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
