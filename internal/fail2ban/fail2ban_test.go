package fail2ban

import (
	"context"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

func fixtures(t *testing.T) collect.Collector {
	t.Helper()
	return collect.NewFixtures(filepath.Join("..", "..", "fixtures", "host"))
}

func TestCollectFixtures(t *testing.T) {
	st := Collect(context.Background(), fixtures(t))
	if !st.Installed || !st.Running || st.Version != "1.0.2" {
		t.Fatalf("state = %+v", st)
	}
	if len(st.Jails) != 3 || st.BannedNow != 5 {
		t.Fatalf("jails = %d, banned = %d", len(st.Jails), st.BannedNow)
	}
	var sshd model.Fail2banJail
	for _, j := range st.Jails {
		if j.Name == "sshd" {
			sshd = j
		}
	}
	if sshd.MaxRetry != 5 || sshd.FindTime != 600 || sshd.BanTime != 3600 || sshd.TotalBanned != 41 || sshd.Failed != 2 {
		t.Fatalf("sshd = %+v", sshd)
	}
	if len(sshd.Bans) != 3 || sshd.Bans[0].Since == "" || sshd.Bans[0].Until == "" {
		t.Fatalf("bans = %+v", sshd.Bans)
	}
	if len(sshd.IgnoreIP) != 3 || !Covers(sshd.IgnoreIP, netip.MustParseAddr("10.1.2.3")) {
		t.Fatalf("ignore = %v", sshd.IgnoreIP)
	}
	light := CollectBans(context.Background(), fixtures(t))
	if light.BannedNow != 5 || light.Version != "" {
		t.Fatalf("light = %+v", light)
	}
}

func TestParseJailStatusJournal(t *testing.T) {
	j := model.Fail2banJail{Name: "sshd"}
	applyJailStatus(&j, "Status for the jail: sshd\n|- Filter\n|  |- Currently failed:\t0\n|  |- Total failed:\t3\n|  `- Journal matches:\t_SYSTEMD_UNIT=sshd.service + _COMM=sshd\n`- Actions\n   |- Currently banned:\t0\n   |- Total banned:\t0\n   `- Banned IP list:\t\n")
	if j.Journal != "_SYSTEMD_UNIT=sshd.service + _COMM=sshd" || len(j.Bans) != 0 || j.TotalFailed != 3 {
		t.Fatalf("%+v", j)
	}
	if got := parseList("No IP address/network is ignored\n"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if v := parseVersion("Fail2Ban v0.10.2\n"); v != "0.10.2" {
		t.Fatal(v)
	}
}

func TestParseBanTimesPermanent(t *testing.T) {
	m := parseBanTimes("1.2.3.4 \t2026-09-29 10:00:00 + -1 = 9999-12-31 23:59:59\n5.6.7.8\t2026-09-29 10:00:00 + 600 = 2026-09-29 10:10:00\n")
	if m["1.2.3.4"][0] == "" || m["1.2.3.4"][1] != "" || m["5.6.7.8"][1] == "" {
		t.Fatalf("%v", m)
	}
}

func TestReadLogFixtures(t *testing.T) {
	c := fixtures(t)
	res := ReadLog(context.Background(), c, LogQuery{Days: 7})
	if res.Source != LogPath || res.Total < 100 || len(res.Events) == 0 {
		t.Fatalf("total=%d source=%s", res.Total, res.Source)
	}
	// Новые сверху.
	if res.Events[0].TS < res.Events[len(res.Events)-1].TS {
		t.Fatal("not sorted newest first")
	}
	bans := ReadLog(context.Background(), c, LogQuery{Days: 7, Action: "Ban", Text: "203.0.113.45"})
	for _, e := range bans.Events {
		if e.Action != "Ban" || e.IP != "203.0.113.45" {
			t.Fatalf("%+v", e)
		}
	}
	if bans.Total == 0 {
		t.Fatal("no bans for 203.0.113.45")
	}
}

func TestHubIgnoreContent(t *testing.T) {
	c := fixtures(t)
	def := DefaultIgnoreIP(c, "/etc/fail2ban")
	if strings.Join(def, " ") != "127.0.0.1/8 ::1 10.0.0.0/8" {
		t.Fatalf("default = %v", def)
	}
	hub := netip.MustParseAddr("198.51.100.7")
	got := HubIgnoreContent(def, hub)
	if !strings.Contains(got, "ignoreip = 127.0.0.1/8 ::1 10.0.0.0/8 198.51.100.7\n") || !strings.Contains(got, "[DEFAULT]") {
		t.Fatal(got)
	}
	// Адрес внутри уже разрешённой сети второй раз не добавляется.
	if strings.Contains(HubIgnoreContent(def, netip.MustParseAddr("10.2.3.4")), "10.2.3.4") {
		t.Fatal("covered address duplicated")
	}
}

func TestParseINIContinuation(t *testing.T) {
	ini := ParseINI("[DEFAULT]\nignoreip = 127.0.0.1/8\n    10.0.0.0/8\n# c\n[sshd]\nenabled: true\n")
	if ini["DEFAULT"]["ignoreip"] != "127.0.0.1/8 10.0.0.0/8" || ini["sshd"]["enabled"] != "true" {
		t.Fatalf("%v", ini)
	}
}

func TestWithFilterAddsLine(t *testing.T) {
	got := WithFilter("myapp", "[myapp]\nenabled = true\n", "[Definition]\nfailregex = x\n")
	if !strings.Contains(got, "[myapp]\nfilter = nkt-myapp\nenabled = true") {
		t.Fatal(got)
	}
	keep := "[myapp]\nfilter = other\n"
	if WithFilter("myapp", keep, "x") != keep {
		t.Fatal("existing filter line replaced")
	}
	if TemplateJailName("# c\n[ my-jail ]\n") != "my-jail" {
		t.Fatal("jail name")
	}
}

func TestFindings(t *testing.T) {
	ssh := []model.Listener{{Protocol: "tcp", Address: "0.0.0.0", Port: 22, Process: "sshd"}}
	if f := Findings(&model.Fail2banState{}, ssh, netip.Addr{}); len(f) != 1 || f[0].ID != "fail2ban-missing" {
		t.Fatalf("%+v", f)
	}
	if f := Findings(&model.Fail2banState{}, []model.Listener{{Address: "127.0.0.1", Port: 22, Process: "sshd"}}, netip.Addr{}); len(f) != 0 {
		t.Fatalf("loopback ssh: %+v", f)
	}
	st := &model.Fail2banState{Installed: true, Running: true, Jails: []model.Fail2banJail{
		{Name: "nginx", LogPaths: []string{"/x"}, MissingLogs: []string{"/x"}, IgnoreIP: []string{"127.0.0.1/8"}},
	}}
	ids := map[string]bool{}
	for _, f := range Findings(st, ssh, netip.MustParseAddr("198.51.100.7")) {
		ids[f.ID] = true
	}
	if !ids["fail2ban-no-sshd"] || !ids["fail2ban-deadjail-nginx"] || !ids["fail2ban-hub-not-ignored"] {
		t.Fatalf("%v", ids)
	}
}

func TestParseIP(t *testing.T) {
	for _, bad := range []string{"", "1.2.3", "1.2.3.4/24", "fe80::1%eth0", "; rm -rf /"} {
		if _, err := ParseIP(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if a, err := ParseIP(" ::ffff:1.2.3.4 "); err != nil || a.String() != "1.2.3.4" {
		t.Fatal(a, err)
	}
	if ValidJailName("sshd; reboot") || !ValidJailName("nginx-http-auth") {
		t.Fatal("jail names")
	}
}
