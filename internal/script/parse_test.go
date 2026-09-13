package script

import (
	"errors"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/msgs"
)

const sample = `# Веб-ферма
set IMAGE ubuntu-24.04
group web-farm profile web-base

host web1 192.0.2.10 user root password ask group web-farm
host web2 192.0.2.11:2222 user deploy key hub
install web1 web2
wait web1 online 2m

on web1 packages install nginx htop   # с комментарием
on web1 service nginx restart
on web1 firewall allow 443/tcp
on web1 firewall allow 5432 from 10.0.0.0/24
on web1 docker install
on web1 docker stack /srv/app/docker-compose.yml up
services:
  web:
    image: nginx:alpine
    command: ["nginx", "-g", "daemon off;"]
end
on web1 vm create app1 image ${IMAGE} cpu 2 mem 2048 disk 20 profile app install
on app1 apply profile app
on web1 vm start app1
on web2 file put /etc/motd mode 0600
Сервер под управлением nkt
end
`

func TestParseSample(t *testing.T) {
	sc, issues := Parse(sample)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	if len(sc.Steps) != 15 {
		t.Fatalf("steps = %d", len(sc.Steps))
	}
	if sc.Vars["IMAGE"] != "ubuntu-24.04" || len(sc.Asks) != 1 || sc.Asks[0] != "web1" {
		t.Errorf("vars/asks: %+v %+v", sc.Vars, sc.Asks)
	}
	if sc.Steps[0].Kind != KindGroup || sc.Steps[0].Args["profile"] != "web-base" {
		t.Errorf("group: %+v", sc.Steps[0])
	}
	h2 := sc.Steps[2]
	if h2.Kind != KindHost || h2.Args["addr"] != "192.0.2.11" || h2.Args["port"] != "2222" || h2.Args["auth"] != "key-hub" {
		t.Errorf("host web2: %+v", h2)
	}
	if sc.Steps[1].Args["auth"] != "password-ask" || sc.Steps[1].Args["port"] != "22" {
		t.Errorf("host web1: %+v", sc.Steps[1])
	}
	pk := sc.Steps[5]
	if pk.Kind != KindPackages || pk.Host != "web1" || strings.Join(pk.List, ",") != "nginx,htop" {
		t.Errorf("packages: %+v", pk)
	}
	fw := sc.Steps[8]
	if fw.Args["port"] != "5432" || fw.Args["proto"] != "tcp" || fw.Args["from"] != "10.0.0.0/24" {
		t.Errorf("firewall: %+v", fw)
	}
	st := sc.Steps[10]
	if st.Kind != KindDockerStack || st.Action != "up" || !strings.Contains(st.Block, "image: nginx:alpine") || !strings.Contains(st.Block, `"daemon off;"`) {
		t.Errorf("stack: %+v", st)
	}
	vm := sc.Steps[11]
	if vm.Kind != KindVMCreate || vm.Name != "app1" || vm.Args["image"] != "ubuntu-24.04" || vm.Args["install"] != "true" || vm.Args["mem"] != "2048" {
		t.Errorf("vm create: %+v", vm)
	}
	fp := sc.Steps[14]
	if fp.Kind != KindFilePut || fp.Args["mode"] != "0600" || fp.Block != "Сервер под управлением nkt\n" {
		t.Errorf("file put: %+v", fp)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"host web1 192.0.2.10":                        "hostNeedsUser",
		"host web1 192.0.2.10 user root":              "hostNeedsAuth",
		"host web1 192.0.2.10 user root key ~/.ssh":   "keyOnlyHub",
		"host web1 bad:port user root key hub":        "badAddr",
		"on web1 packages install Nginx!":             "badPackageName",
		"on web1 firewall allow 70000":                "badPort",
		"on web1 vm create x cpu 2":                   "vmNeedsImage",
		"on web1 vm create x image u cpu two":         "badNumber",
		"on web1 docker stack relative.yml up":        "badDockerStack",
		"on web1 file put /etc/motd\nтекст":           "blockNotClosed",
		"end":                                         "endWithoutBlock",
		"on web1 vm create x image ${IMG}":            "unknownVar",
		"frobnicate":                                  "unknownCommand",
		"on web1 service nginx dance":                 "badService",
		"host web1 192.0.2.10 user root password \"a": "unclosedQuote",
	}
	for text, want := range cases {
		_, issues := Parse(text)
		if len(issues) == 0 {
			t.Errorf("%q: ошибок нет, ожидалось %s", text, want)
			continue
		}
		if !strings.Contains(issueKey(issues[0]), want) {
			t.Errorf("%q: %v, ожидалось %s", text, issues[0].Err, want)
		}
	}
}

func TestCheckRefs(t *testing.T) {
	sc, issues := Parse(sample)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	refs := Refs{Hosts: map[string]bool{"old": true}, Groups: map[string]bool{}, Profiles: map[string]bool{"web-base": true, "app": true}}
	if got := Check(sc, refs); len(got) != 0 {
		t.Errorf("чистый сценарий: %+v", got)
	}
	refs.Profiles = map[string]bool{}
	refs.Hosts["web1"] = true
	got := Check(sc, refs)
	keys := ""
	for _, i := range got {
		keys += issueKey(i) + " "
	}
	for _, want := range []string{"unknownProfile", "hostExists"} {
		if !strings.Contains(keys, want) {
			t.Errorf("нет %s в %s", want, keys)
		}
	}
	// Обращение к хосту, которого нет ни в хабе, ни выше.
	sc2, _ := Parse("on ghost packages install htop")
	if got := Check(sc2, refs); len(got) != 1 || !strings.Contains(issueKey(got[0]), "unknownHost") {
		t.Errorf("неизвестный хост: %+v", got)
	}
}

func issueKey(i Issue) string {
	var e *msgs.Err
	if errors.As(i.Err, &e) {
		return e.Key
	}
	return i.Err.Error()
}
