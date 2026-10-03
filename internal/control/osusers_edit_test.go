package control

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

func osUsersFixture(t *testing.T, hubUser string) (*OSUserManager, *[][]string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"etc/passwd": "root:x:0:0:root:/root:/bin/bash\n" +
			"alice:x:1000:1000::/home/alice:/bin/bash\n" +
			"deploy:x:1001:1001::/home/deploy:/bin/bash\n",
		"etc/group": "root:x:0:\nsudo:x:27:deploy\ndocker:x:999:alice,deploy\nadm:x:4:alice\n" +
			"alice:x:1000:\ndeploy:x:1001:\n",
		"etc/shells":                       "# shells\n/bin/sh\n/bin/bash\n/usr/bin/zsh\n",
		"etc/sudoers.d/nkt-hub":            "deploy ALL=(root) NOPASSWD: /usr/local/bin/nkt hub-sudo\n",
		"etc/sudoers.d/nkt-alice":          "alice ALL=(ALL) NOPASSWD: ALL\n",
		"home/alice/.ssh/authorized_keys":  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA one\n# note\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB two\n",
		"home/deploy/.ssh/authorized_keys": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC hub\n",
	}
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var calls [][]string
	m := NewOSUserManager(collect.NewFixtures(root), func(_ context.Context, argv ...string) (collect.CommandResult, error) {
		calls = append(calls, argv)
		return collect.CommandResult{Argv: argv}, nil
	}).WithHubUser(hubUser)
	return m, &calls
}

func userByName(t *testing.T, m *OSUserManager, name string) OSUser {
	t.Helper()
	list, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range list {
		if u.Name == name {
			return u
		}
	}
	t.Fatalf("no user %s", name)
	return OSUser{}
}

func TestOSUsersListGroupsAndHub(t *testing.T) {
	m, _ := osUsersFixture(t, "")
	a := userByName(t, m, "alice")
	if !slices.Equal(a.Groups, []string{"adm", "docker"}) || !a.NktSudo || a.HubUser {
		t.Fatalf("alice: %+v", a)
	}
	d := userByName(t, m, "deploy")
	if !d.HubUser || d.NktSudo || !slices.Contains(d.Groups, "sudo") {
		t.Fatalf("deploy: %+v", d)
	}
	if len(a.Keys) != 2 || a.Keys[0].ID == "" || a.Keys[0].ID == a.Keys[1].ID {
		t.Fatalf("key ids: %+v", a.Keys)
	}
	groups, _ := m.Groups(context.Background())
	if len(groups) != 6 || groups[0].Name != "adm" || !groups[0].System {
		t.Fatalf("groups: %+v", groups)
	}
}

func TestOSUserUpdate(t *testing.T) {
	m, calls := osUsersFixture(t, "")
	ctx := context.Background()
	a := userByName(t, m, "alice")
	groups := []string{"docker", "sudo", "docker"}
	shell := "/usr/bin/zsh"
	off := false
	err := m.Update(ctx, "alice", UpdateOptions{Groups: &groups, Shell: &shell, Sudo: &off, RemoveKeys: []string{a.Keys[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	joined := make([]string, len(*calls))
	for i, c := range *calls {
		joined[i] = strings.Join(c, " ")
	}
	if joined[0] != "usermod -G docker,sudo alice" || joined[1] != "usermod -s /usr/bin/zsh alice" ||
		joined[2] != "rm -f /etc/sudoers.d/nkt-alice" {
		t.Fatalf("calls: %q", joined)
	}
	// Ключ убран, второй и комментарий остались.
	last := joined[3]
	if strings.Contains(last, "AAAAIAAAA") || !strings.Contains(last, "AAAAIBBBB") || !strings.Contains(last, "# note") {
		t.Fatalf("rewrite: %s", last)
	}

	bad := []string{"nope"}
	if err := m.Update(ctx, "alice", UpdateOptions{Groups: &bad}); err == nil {
		t.Fatal("unknown group accepted")
	}
	sh := "/bin/evil"
	if err := m.Update(ctx, "alice", UpdateOptions{Shell: &sh}); err == nil {
		t.Fatal("unknown shell accepted")
	}
	if err := m.Update(ctx, "alice", UpdateOptions{RemoveKeys: []string{"x; rm"}}); err == nil {
		t.Fatal("bad key id accepted")
	}
}

func TestOSUserHubProtection(t *testing.T) {
	m, calls := osUsersFixture(t, "")
	ctx := context.Background()
	d := userByName(t, m, "deploy")
	none := []string{"docker"}
	if err := m.Update(ctx, "deploy", UpdateOptions{Groups: &none}); err == nil {
		t.Fatal("hub user lost sudo group without confirm")
	}
	if err := m.Update(ctx, "deploy", UpdateOptions{RemoveKeys: []string{d.Keys[0].ID}}); err == nil {
		t.Fatal("hub key removed without confirm")
	}
	if len(*calls) != 0 {
		t.Fatalf("ran: %q", *calls)
	}
	if err := m.Update(ctx, "deploy", UpdateOptions{Groups: &none, ConfirmHub: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "deploy", true); err == nil {
		t.Fatal("hub user deleted")
	}
	if err := m.Delete(ctx, "root", false); err == nil {
		t.Fatal("root deleted")
	}
	*calls = nil
	if err := m.Delete(ctx, "alice", true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join((*calls)[len(*calls)-1], " "); got != "userdel -r alice" {
		t.Fatalf("delete: %s", got)
	}
	// Пользователь хаба из настроек службы.
	m2, _ := osUsersFixture(t, "alice")
	if err := m2.Delete(ctx, "alice", false); err == nil {
		t.Fatal("configured hub user deleted")
	}
}

func TestOSUserCreateGroups(t *testing.T) {
	m, calls := osUsersFixture(t, "")
	ctx := context.Background()
	if err := m.Create(ctx, CreateOptions{Name: "bob", Groups: []string{"docker", "adm"}, Shell: "/usr/bin/zsh"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0][2], "--shell '/usr/bin/zsh' bob") ||
		strings.Join((*calls)[1], " ") != "usermod -aG docker,adm bob" {
		t.Fatalf("calls: %q", *calls)
	}
	if err := m.Create(ctx, CreateOptions{Name: "bob", Groups: []string{"wheel"}}); err == nil {
		t.Fatal("missing group accepted")
	}
}
