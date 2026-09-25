package api

import (
	"strings"
	"testing"
)

// Сценарий смены пароля: пароля в нём нет (только {secret}), имя
// гостя в кавычках, для root пользователь не заводится.
func TestGuestPasswordCommand(t *testing.T) {
	c := guestPasswordCommand("lxd", "c1", "admin")
	if c.SecretRef != "lxd:c1" || !strings.Contains(c.Script, "{secret} |") || !strings.Contains(c.Script, "useradd -m -s /bin/bash admin") {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(c.Script, "${") {
		t.Error("${ в сценарии")
	}
	if strings.Contains(guestPasswordCommand("lxd", "c1", "root").Script, "useradd") {
		t.Error("root заводится")
	}
	if v := guestPasswordCommand("vm", "web", "debian"); !strings.Contains(v.Script, "set-user-password 'web' debian {secret}") {
		t.Errorf("%s", v.Script)
	}
}
