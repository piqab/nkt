package hub

import (
	"context"
	"os/exec"
	osuser "os/user"
	"slices"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/store"
)

func TestParseSudoList(t *testing.T) {
	out := `Matching Defaults entries for deploy on h:
    env_reset, secure_path=/usr/bin

User deploy may run the following commands on h:
    (root) NOPASSWD: /usr/local/bin/nkt hub-sudo
    (ALL : ALL) ALL
`
	full, rules, narrow := parseSudoList(out)
	if full || len(rules) != 0 || !narrow {
		t.Fatalf("narrow only: %v %v %v", full, rules, narrow)
	}
	full, rules, narrow = parseSudoList(out + "    (ALL) NOPASSWD: SETENV: ALL\n")
	if !full || len(rules) != 1 || !narrow {
		t.Fatalf("other full rule: %v %v %v", full, rules, narrow)
	}
	if full, _, _ := parseSudoList("sudo: a password is required"); full {
		t.Fatal("no list")
	}
	if full, _, _ := parseSudoList("may run the following commands\n (ALL) NOPASSWD: /bin/ls, /usr/bin/ALLx\n"); full {
		t.Fatal("ALL as part of a path")
	}
}

func TestClassifySudoLine(t *testing.T) {
	groups := []string{"deploy", "admin"}
	for text, want := range map[string]string{
		"deploy ALL=(ALL) NOPASSWD:ALL":                           "user",
		"deploy ALL=(ALL) NOPASSWD: ALL":                          "user",
		"ops,deploy ALL=(ALL) NOPASSWD: ALL":                      "user",
		"%admin ALL=(ALL) NOPASSWD: ALL":                          "group",
		"%wheel ALL=(ALL) NOPASSWD: ALL":                          "",
		"ALL ALL=(ALL) NOPASSWD: ALL":                             "all",
		"ADMINS ALL=(ALL) NOPASSWD: ALL":                          "alias",
		"deploy ALL=(root) NOPASSWD: /usr/local/bin/nkt hub-sudo": "",
		"deploy ALL=(ALL) ALL":                                    "",
		"# deploy ALL=(ALL) NOPASSWD: ALL":                        "",
		"User_Alias ADMINS = deploy":                              "",
		"Defaults:deploy !requiretty":                             "",
		"alice ALL=(ALL) NOPASSWD: ALL":                           "",
	} {
		if got := classifySudoLine(text, "deploy", groups); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

// Здесь sudo требует пароль: проверка честно говорит «нужен пароль», а не
// «полный sudo», и не требует root.
func TestProbeSudoPasswordRequired(t *testing.T) {
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Skip("no sudo")
	}
	addr, port, keyPEM := startTestSSHD(t)
	me, _ := osuser.Current()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := dialSSH(ctx, addr, port, me.Username, store.HostAuthKey, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	m, _ := newTestManager(t)
	st, err := m.probeSudo(client, me.Username)
	if err != nil {
		t.Skipf("sudo here is not the password-required kind: %v", err)
	}
	if st.Full {
		t.Skip("passwordless sudo on this machine")
	}
	if st.Status != store.SudoStatusPasswordRequired || st.HubKey || !slices.Contains([]string{"", "P", "L", "NP"}, st.Password) {
		t.Fatalf("state: %+v", st)
	}
}

// После операции значок пишется по проверке, а не по догадке: здесь sudo
// просит пароль — запомненное «без пароля» заменяется честным состоянием.
func TestRefreshSudoStatusOverridesGuess(t *testing.T) {
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Skip("no sudo")
	}
	addr, port, keyPEM := startTestSSHD(t)
	me, _ := osuser.Current()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := dialSSH(ctx, addr, port, me.Username, store.HostAuthKey, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	m, db := newTestManager(t)
	id, err := db.CreateHost(ctx, "h", addr, port, me.Username, store.HostAuthKey, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if st, err := m.probeSudo(client, me.Username); err != nil || st.Full {
		t.Skipf("sudo here is not the password-required kind: %+v %v", st, err)
	}
	if got := m.refreshSudoStatus(ctx, id, client, me.Username, store.SudoStatusNarrow); got != store.SudoStatusPasswordRequired {
		t.Fatalf("status %q", got)
	}
	h, _ := db.HostByID(ctx, id)
	if h.SudoStatus != store.SudoStatusPasswordRequired {
		t.Fatalf("stored %q", h.SudoStatus)
	}
}
