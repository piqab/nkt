// Package ups — источники бесперебойного питания через NUT (Network UPS
// Tools): состояние по upsc, поиск ИБП на USB и COM, следы UPSilon 2000,
// который держит порт ИБП и мешает драйверу NUT.
//
// nkt ИБП напрямую не читает: протоколы (Megatec Q1, HID, …) — дело
// драйверов NUT, а nkt ставит и настраивает NUT и читает upsc. upsd
// слушает только 127.0.0.1, наружу ничего не открывается; хаб узнаёт о ИБП
// из обычного опроса хоста.
package ups

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// ParseUpsc — вывод «upsc имя»: строки «ключ: значение».
func ParseUpsc(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k != "" && !strings.ContainsAny(k, " \t") {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

func num(vars map[string]string, key string) float64 {
	f, _ := strconv.ParseFloat(vars[key], 64)
	return f
}

// FromVars — ИБП по переменным NUT. ups.status — флаги через пробел: OL
// (от сети), OB (от батареи), LB (батарея садится), RB (заменить
// батарею), CHRG, DISCHRG, OVER (перегрузка), BYPASS, OFF.
func FromVars(name string, vars map[string]string) model.UPS {
	u := model.UPS{
		Name:          name,
		Model:         strings.TrimSpace(firstOf(vars, "device.model", "ups.model")),
		Manufacturer:  strings.TrimSpace(firstOf(vars, "device.mfr", "ups.mfr")),
		Driver:        vars["driver.name"],
		Status:        vars["ups.status"],
		ChargePct:     num(vars, "battery.charge"),
		LoadPct:       num(vars, "ups.load"),
		RuntimeS:      int64(num(vars, "battery.runtime")),
		InputV:        num(vars, "input.voltage"),
		OutputV:       num(vars, "output.voltage"),
		BatteryV:      num(vars, "battery.voltage"),
		Firmware:      firstOf(vars, "ups.firmware", "device.firmware"),
		Serial:        firstOf(vars, "device.serial", "ups.serial"),
		Beeper:        vars["ups.beeper.status"],
		HasCharge:     vars["battery.charge"] != "",
		HasRuntime:    vars["battery.runtime"] != "",
		LowChargePct:  num(vars, "battery.charge.low"),
		LowRuntimeS:   int64(num(vars, "battery.runtime.low")),
		NominalPowerW: num(vars, "ups.realpower.nominal"),
	}
	flags := strings.Fields(u.Status)
	has := func(f string) bool {
		for _, x := range flags {
			if x == f {
				return true
			}
		}
		return false
	}
	u.OnBattery = has("OB")
	u.LowBattery = has("LB")
	u.ReplaceBattery = has("RB")
	u.Overload = has("OVER")
	return u
}

func firstOf(vars map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := vars[k]; v != "" {
			return v
		}
	}
	return ""
}

// Status — ИБП, которые знает NUT на этом хосте (upsc -l и upsc имя).
// NUT не стоит — nil; ИБП описан, а драйвер не отвечает — с Error.
func Status(ctx context.Context, c collect.Collector) []model.UPS {
	if !collect.Which(ctx, c, "upsc") {
		return nil
	}
	list, err := c.Run(ctx, "upsc", "-l", "localhost")
	if err != nil || !list.OK() {
		return nil
	}
	var out []model.UPS
	for _, name := range strings.Fields(list.Stdout) {
		if !nameRe.MatchString(name) {
			continue
		}
		res, err := c.Run(ctx, "upsc", name+"@localhost")
		if err != nil || !res.OK() {
			msg := strings.TrimSpace(res.Stderr + res.Stdout)
			if err != nil {
				msg = err.Error()
			}
			out = append(out, model.UPS{Name: name, Error: firstLine(msg)})
			continue
		}
		out = append(out, FromVars(name, ParseUpsc(res.Stdout)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}

// nameRe — имя ИБП в NUT (секция ups.conf).
var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidName — допустимое имя ИБП.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// knownUSB — ИБП на USB: код производителя (и, если важно, изделия) →
// драйвер NUT и как его назвать. Megatec/Cypress 0665:5161 — Ippon,
// Powercom, SVEN и прочие ИБП, с которыми работает UPSilon 2000.
var knownUSB = []struct {
	vendor, product, driver, label string
}{
	{"0665", "5161", "nutdrv_qx", "Megatec/Cypress (Ippon, SVEN, Powercom… — UPSilon 2000)"},
	{"0001", "0000", "nutdrv_qx", "Megatec (Krauler, Gembird…)"},
	{"06da", "", "nutdrv_qx", "Phoenixtec (Ippon, Liebert…)"},
	{"0925", "1234", "nutdrv_qx", "Richcomm (Powercom, Atlantis…)"},
	{"0d9f", "", "usbhid-ups", "Powercom"},
	{"051d", "", "usbhid-ups", "APC"},
	{"0463", "", "usbhid-ups", "Eaton / MGE"},
	{"0764", "", "usbhid-ups", "CyberPower"},
	{"09ae", "", "usbhid-ups", "Tripp Lite"},
	{"10af", "", "usbhid-ups", "Liebert"},
	{"2b2d", "", "usbhid-ups", "Ippon (HID)"},
	{"075d", "", "usbhid-ups", "Ippon (HID)"},
}

// USBDevice — USB-устройство, похожее на ИБП.
type USBDevice struct {
	Vendor       string `json:"vendor"`
	Product      string `json:"product"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Name         string `json:"name,omitempty"`
	Driver       string `json:"driver"`
	Label        string `json:"label"`
}

// UPSilon — следы UPSilon 2000 на хосте.
type UPSilon struct {
	Present bool     `json:"present"`
	Running bool     `json:"running"`
	Units   []string `json:"units,omitempty"`
	Paths   []string `json:"paths,omitempty"`
}

// Detection — что нашлось на хосте (без изменений).
type Detection struct {
	USB     []USBDevice `json:"usb"`
	Serial  []string    `json:"serial"`
	UPSilon UPSilon     `json:"upsilon"`
	// NUTInstalled — стоит пакет NUT (есть upsc и upsdrvctl/драйверы);
	// NUTConfigured — в ups.conf уже описан ИБП.
	NUTInstalled  bool        `json:"nut_installed"`
	NUTConfigured bool        `json:"nut_configured"`
	UPS           []model.UPS `json:"ups,omitempty"`
	// AptGet — пакет nut можно поставить (apt-get есть).
	AptGet bool `json:"apt_get"`
}

// Detect — поиск ИБП: USB по /sys/bus/usb/devices (коды производителя
// и изделия), COM-порты, UPSilon 2000, состояние NUT. Ничего не меняет.
func Detect(ctx context.Context, c collect.Collector) Detection {
	d := Detection{USB: []USBDevice{}, Serial: []string{}}
	devs, _ := c.Glob("/sys/bus/usb/devices/*/idVendor")
	sort.Strings(devs)
	for _, vf := range devs {
		dir := path.Dir(vf)
		vendor := strings.ToLower(readTrim(c, vf))
		product := strings.ToLower(readTrim(c, dir+"/idProduct"))
		for _, k := range knownUSB {
			if vendor != k.vendor || (k.product != "" && product != k.product) {
				continue
			}
			d.USB = append(d.USB, USBDevice{
				Vendor: vendor, Product: product,
				Manufacturer: readTrim(c, dir+"/manufacturer"), Name: readTrim(c, dir+"/product"),
				Driver: k.driver, Label: k.label,
			})
			break
		}
	}
	for _, pat := range []string{"/dev/ttyS*", "/dev/ttyUSB*", "/dev/ttyACM*"} {
		if m, err := c.Glob(pat); err == nil {
			d.Serial = append(d.Serial, m...)
		}
	}
	sort.Strings(d.Serial)
	d.UPSilon = detectUPSilon(ctx, c)
	d.NUTInstalled = collect.Which(ctx, c, "upsc") && (c.Exists("/lib/nut") || c.Exists("/usr/lib/nut") || c.Exists("/etc/nut"))
	if raw, err := c.ReadFile("/etc/nut/ups.conf"); err == nil {
		d.NUTConfigured = upsConfSections(string(raw)) > 0
	}
	if d.NUTInstalled {
		d.UPS = Status(ctx, c)
	}
	d.AptGet = collect.Which(ctx, c, "apt-get")
	return d
}

func readTrim(c collect.Collector, p string) string {
	b, err := c.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// upsConfSections — сколько ИБП описано в ups.conf ([имя], кроме
// пустых и комментариев).
func upsConfSections(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && len(line) > 2 {
			n++
		}
	}
	return n
}

// detectUPSilon — UPSilon 2000: процесс, службы systemd и init.d,
// каталоги установки.
func detectUPSilon(ctx context.Context, c collect.Collector) UPSilon {
	var u UPSilon
	if res, err := c.Run(ctx, "pgrep", "-i", "upsilon"); err == nil && res.OK() && strings.TrimSpace(res.Stdout) != "" {
		u.Running = true
	}
	for _, pat := range []string{"/etc/systemd/system/*upsilon*.service", "/lib/systemd/system/*upsilon*.service", "/usr/lib/systemd/system/*upsilon*.service", "/etc/init.d/*upsilon*"} {
		m, _ := c.Glob(pat)
		for _, p := range m {
			u.Units = append(u.Units, p)
		}
	}
	for _, p := range []string{"/usr/local/upsilon", "/opt/upsilon", "/etc/upsilon", "/usr/upsilon"} {
		if c.Exists(p) {
			u.Paths = append(u.Paths, p)
		}
	}
	u.Present = u.Running || len(u.Units) > 0 || len(u.Paths) > 0
	return u
}

// DriverFor — драйвер NUT для USB-кода; "" — неизвестен.
func DriverFor(vendor, product string) string {
	for _, k := range knownUSB {
		if vendor == k.vendor && (k.product == "" || product == k.product) {
			return k.driver
		}
	}
	return ""
}

// Drivers — драйверы, которые мастер умеет настроить.
var Drivers = []string{"nutdrv_qx", "usbhid-ups", "blazer_usb", "blazer_ser"}
