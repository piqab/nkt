package api

import (
	"net/url"
	"reflect"
	"testing"
)

// Параметры подключения ИБП попадают в сценарий только проверенными.
func TestUPSConnectEnv(t *testing.T) {
	ok := []string{
		"driver=nutdrv_qx&port=auto&vendor=0665&product=5161",
		"driver=nutdrv_qx&port=/dev/ttyUSB0&pid=812&autostart=/etc/rc.local",
		"driver=blazer_ser&port=/dev/ttyS0&shutdown=1",
		"driver=usbhid-ups&autostart=/etc/init.d/upsilon&autostart=/lib/systemd/system/upsilon.service&autostart=/etc/cron.d/upsilon",
	}
	for _, q := range ok {
		v, _ := url.ParseQuery(q)
		if _, good := upsConnectEnv(v); !good {
			t.Errorf("отвергнуто: %s", q)
		}
	}
	bad := []string{
		"driver=rm -rf /&port=auto",
		"driver=nutdrv_qx&port=/etc/passwd",
		"driver=nutdrv_qx&port=auto&vendor=06655",
		"driver=blazer_ser&port=auto",
		"driver=usbhid-ups&port=/dev/ttyS0",
		"driver=nutdrv_qx&autostart=/etc/init.d/../../bin/sh",
		"driver=nutdrv_qx&autostart=/etc/passwd",
		"driver=nutdrv_qx&pid=1",
		"driver=nutdrv_qx&pid=12a",
	}
	for _, q := range bad {
		v, _ := url.ParseQuery(q)
		if _, good := upsConnectEnv(v); good {
			t.Errorf("пропущено: %s", q)
		}
	}
	// Значения целиком, мимо разбора адреса: ParseQuery «;» сам отбрасывает.
	for _, u := range []string{"/etc/rc.local;reboot", "/etc/init.d/up silon", "/etc/init.d/$(id)"} {
		if _, good := upsConnectEnv(url.Values{"driver": {"nutdrv_qx"}, "autostart": {u}}); good {
			t.Errorf("пропущено: %q", u)
		}
	}
	v, _ := url.ParseQuery("driver=nutdrv_qx&port=/dev/ttyUSB0&shutdown=1&autostart=/etc/rc.local&pid=812&pid=813")
	env, _ := upsConnectEnv(v)
	if env["NKT_UPS_SHUTDOWN"] != "1" || env["NKT_UPS_AUTOSTART"] != "/etc/rc.local" || env["NKT_UPS_PIDS"] != "812 813" {
		t.Fatal(env)
	}
	if env["NKT_UPS_CANDIDATES"] != "nutdrv_qx|/dev/ttyUSB0|| blazer_ser|/dev/ttyUSB0|| nutdrv_qx|auto||" {
		t.Fatalf("%q", env["NKT_UPS_CANDIDATES"])
	}
}

// Варианты: выбранный, тот же порт другим драйвером Megatec, nutdrv_qx по
// USB; USB-драйвер без порта — только auto.
func TestUPSCandidates(t *testing.T) {
	cases := []struct {
		driver, port, vendor, product string
		want                          []string
	}{
		{"blazer_ser", "/dev/ttyS0", "", "", []string{"blazer_ser|/dev/ttyS0||", "nutdrv_qx|/dev/ttyS0||", "nutdrv_qx|auto||"}},
		{"nutdrv_qx", "auto", "0665", "5161", []string{"nutdrv_qx|auto|0665|5161", "blazer_usb|auto|0665|5161"}},
		{"usbhid-ups", "auto", "051d", "0002", []string{"usbhid-ups|auto|051d|0002"}},
	}
	for _, c := range cases {
		if got := upsCandidates(c.driver, c.port, c.vendor, c.product); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s: %v, ждали %v", c.driver, c.port, got, c.want)
		}
	}
}
