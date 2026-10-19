package deploy

import "testing"

func TestWithoutHosts(t *testing.T) {
	flow := "action: compose\ncompose:\n  project: app\n  hosts: [web-1, Web-2, db-1]   # куда\n  file: c.yml\n"
	got, ok := WithoutHosts(flow, []string{"web-2"})
	if !ok || got != "action: compose\ncompose:\n  project: app\n  hosts: [web-1, db-1]   # куда\n  file: c.yml\n" {
		t.Fatalf("flow: %v %q", ok, got)
	}
	block := "compose:\n  hosts:\n    - web-1\n    - db-1 # база\n  file: c.yml\n"
	got, ok = WithoutHosts(block, []string{"web-1"})
	if !ok || got != "compose:\n  hosts:\n    - db-1 # база\n  file: c.yml\n" {
		t.Fatalf("block: %v %q", ok, got)
	}
	if _, ok := WithoutHosts(flow, []string{"web-1", "web-2", "db-1"}); ok {
		t.Fatal("последний хост убирать нельзя")
	}
	if _, ok := WithoutHosts("compose:\n  group: prod\n", []string{"x"}); ok {
		t.Fatal("группа — не список")
	}
}

func TestReplaceHost(t *testing.T) {
	flow := "action: compose\ncompose:\n  project: shop\n  hosts: [web1, \"web2\"]   # где\n"
	got, ok := ReplaceHost(flow, "WEB2", "web3")
	if !ok || got != "action: compose\ncompose:\n  project: shop\n  hosts: [web1, web3]   # где\n" {
		t.Fatalf("flow: %v %q", ok, got)
	}
	if _, ok := ReplaceHost(flow, "web1", "web2"); ok {
		t.Fatal("to already there")
	}
	if _, ok := ReplaceHost(flow, "nope", "web3"); ok {
		t.Fatal("from missing")
	}
	block := "compose:\n  hosts:\n    - web1   # основной\n    - web2\n  project: shop\n"
	got, ok = ReplaceHost(block, "web1", "web9")
	if !ok || got != "compose:\n  hosts:\n    - web9 # основной\n    - web2\n  project: shop\n" {
		t.Fatalf("block: %v %q", ok, got)
	}
	if _, ok := ReplaceHost("compose:\n  group: prod\n", "web1", "web2"); ok {
		t.Fatal("group")
	}
}
