package ups

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// Поиск на фикстурах: UPSilon (rupsd из /etc/upsilon, автозапуск в
// rc.local, порт в upsilon.ini), переходник PL2303 → /dev/ttyUSB0,
// рекомендация — порт из настроек UPSilon.
func TestDetectFixtures(t *testing.T) {
	c := collect.NewFixtures("../../fixtures/host")
	d := Detect(context.Background(), c)
	u := d.UPSilon
	if !u.Running || len(u.Procs) != 1 || u.Procs[0].PID != 812 || u.Procs[0].Cmd != "/etc/upsilon/rupsd" {
		t.Fatalf("процессы: %+v", u.Procs)
	}
	if len(u.Autostart) != 1 || u.Autostart[0].Path != "/etc/rc.local" || u.Autostart[0].Kind != "rclocal" {
		t.Fatalf("автозапуск: %+v", u.Autostart)
	}
	if u.Port != "/dev/ttyUSB0" || u.PortFrom != "/etc/upsilon/upsilon.ini" {
		t.Fatalf("порт: %q из %q", u.Port, u.PortFrom)
	}
	if len(d.Adapters) != 1 || d.Adapters[0].Chip != "Prolific PL2303" || d.Adapters[0].Port != "/dev/ttyUSB0" {
		t.Fatalf("переходники: %+v", d.Adapters)
	}
	if r := d.Recommended; r.Driver != "nutdrv_qx" || r.Port != "/dev/ttyUSB0" || r.Reason != "upsilon" {
		t.Fatalf("рекомендация: %+v", r)
	}
}

func TestParsePgrepAndRecommend(t *testing.T) {
	got := parsePgrep("812 /etc/upsilon/rupsd\n900 pgrep -a -f -i upsilon|rupsd\n1 init\nabc x\n913 /usr/bin/upsilon --tray\n")
	if len(got) != 2 || got[1].Cmd != "/usr/bin/upsilon --tray" {
		t.Fatalf("%+v", got)
	}
	// Без UPSilon: переходник → его порт; иначе ИБП на USB; иначе COM.
	if r := recommend(Detection{Adapters: []Adapter{{Port: "/dev/ttyUSB1"}}}); r.Port != "/dev/ttyUSB1" || r.Reason != "adapter" {
		t.Fatal(r)
	}
	if r := recommend(Detection{USB: []USBDevice{{Driver: "usbhid-ups"}}}); r.Driver != "usbhid-ups" || r.Port != "auto" {
		t.Fatal(r)
	}
	if r := recommend(Detection{Serial: []string{"/dev/ttyS0", "/dev/ttyUSB0"}}); r.Port != "/dev/ttyUSB0" {
		t.Fatal(r)
	}
}
