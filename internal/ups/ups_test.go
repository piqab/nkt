package ups

import (
	"testing"

	"github.com/piqab/nkt/internal/model"
)

// Вывод upsc ИБП Megatec (nutdrv_qx), как у Ippon на UPSilon.
const upscOnBattery = `battery.charge: 24
battery.runtime: 180
battery.voltage: 23.10
device.mfr: Ippon
device.model: Back Power Pro 800
driver.name: nutdrv_qx
input.voltage: 0.0
output.voltage: 220.1
ups.load: 41
ups.status: OB DISCHRG
ups.beeper.status: enabled
`

func TestFromVarsAndFindings(t *testing.T) {
	u := FromVars("ups", ParseUpsc(upscOnBattery))
	if !u.OnBattery || u.LowBattery || u.ChargePct != 24 || u.RuntimeS != 180 || u.Model != "Back Power Pro 800" || u.Manufacturer != "Ippon" || u.Driver != "nutdrv_qx" {
		t.Fatalf("%+v", u)
	}
	// 24% < 30% — вот-вот сядет: критично.
	f := Findings([]model.UPS{u}, 30, 5)
	if len(f) != 1 || f[0].Severity != "critical" || f[0].TitleKey != "finding.upsBatteryLow.title" {
		t.Fatalf("%+v", f)
	}
	// Заряд высокий, времени много — просто «от батареи».
	u.ChargePct, u.RuntimeS = 90, 1800
	if f := Findings([]model.UPS{u}, 30, 5); len(f) != 1 || f[0].Severity != "high" || f[0].TitleKey != "finding.upsOnBattery.title" {
		t.Fatalf("%+v", f)
	}
	ol := FromVars("ups", ParseUpsc("ups.status: OL CHRG RB\nbattery.charge: 100\n"))
	if f := Findings([]model.UPS{ol}, 30, 5); len(f) != 1 || f[0].TitleKey != "finding.upsReplaceBattery.title" {
		t.Fatalf("%+v", f)
	}
	if f := Findings([]model.UPS{{Name: "ups", Error: "Driver not connected"}}, 30, 5); len(f) != 1 || f[0].TitleKey != "finding.upsNoComm.title" {
		t.Fatalf("%+v", f)
	}
	if DriverFor("0665", "5161") != "nutdrv_qx" || DriverFor("051d", "0002") != "usbhid-ups" || DriverFor("1234", "5678") != "" {
		t.Fatal("DriverFor")
	}
	if upsConfSections("# c\n[ups]\n driver = x\n") != 1 || upsConfSections("# пусто\n") != 0 {
		t.Fatal("ups.conf")
	}
}
