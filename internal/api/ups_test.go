package api

import (
	"net/url"
	"testing"
)

// Параметры подключения ИБП попадают в сценарий только проверенными.
func TestUPSConnectEnv(t *testing.T) {
	ok := []string{
		"driver=nutdrv_qx&port=auto&vendor=0665&product=5161",
		"driver=nutdrv_qx&port=/dev/ttyUSB0",
		"driver=blazer_ser&port=/dev/ttyS0&shutdown=1",
		"driver=usbhid-ups&upsilon=/etc/init.d/upsilon&upsilon=/lib/systemd/system/upsilon.service",
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
		"driver=nutdrv_qx&upsilon=/etc/init.d/../../bin/sh",
	}
	for _, q := range bad {
		v, _ := url.ParseQuery(q)
		if _, good := upsConnectEnv(v); good {
			t.Errorf("пропущено: %s", q)
		}
	}
	// Значения целиком, мимо разбора адреса: ParseQuery «;» сам отбрасывает.
	for _, u := range []string{"/tmp/x;reboot", "/etc/init.d/up silon", "/etc/init.d/$(id)"} {
		if _, good := upsConnectEnv(url.Values{"driver": {"nutdrv_qx"}, "upsilon": {u}}); good {
			t.Errorf("пропущено: %q", u)
		}
	}
	v, _ := url.ParseQuery("driver=nutdrv_qx&shutdown=1&upsilon=/etc/init.d/upsilon")
	env, _ := upsConnectEnv(v)
	if env["NKT_UPS_PORT"] != "auto" || env["NKT_UPS_SHUTDOWN"] != "1" || env["NKT_UPS_UPSILON"] != "/etc/init.d/upsilon" {
		t.Fatal(env)
	}
}
