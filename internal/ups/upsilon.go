package ups

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/collect"
)

// UPSilon 2000 для Linux: каталог /etc/upsilon, фоновый процесс rupsd (и
// upsilon — его окно), запуск — службой, init.d, rc.local или cron. Он
// держит порт ИБП: пока работает, драйвер NUT ИБП не увидит.

// UPSilonProc — работающий процесс UPSilon.
type UPSilonProc struct {
	PID int    `json:"pid"`
	Cmd string `json:"cmd"`
}

// Autostart — откуда UPSilon запускается при загрузке.
type Autostart struct {
	Path string `json:"path"`
	// Kind — systemd, initd, rclocal, cron.
	Kind string `json:"kind"`
	// Line — строка с запуском (rc.local, cron).
	Line string `json:"line,omitempty"`
}

// UPSilon — следы UPSilon 2000 на хосте.
type UPSilon struct {
	Present   bool          `json:"present"`
	Running   bool          `json:"running"`
	Procs     []UPSilonProc `json:"procs,omitempty"`
	Autostart []Autostart   `json:"autostart,omitempty"`
	Paths     []string      `json:"paths,omitempty"`
	// Port — порт ИБП из настроек UPSilon (/dev/ttyUSB0, COM1 → /dev/ttyS0).
	Port string `json:"port,omitempty"`
	// PortFrom — файл, из которого взят порт.
	PortFrom string `json:"port_from,omitempty"`
}

// upsilonRe — упоминание UPSilon в процессе, службе или строке запуска.
var upsilonRe = regexp.MustCompile(`(?i)(upsilon|\brupsd\b)`)

// Каталоги установки UPSilon.
var upsilonDirs = []string{"/etc/upsilon", "/usr/local/upsilon", "/opt/upsilon", "/usr/upsilon"}

func detectUPSilon(ctx context.Context, c collect.Collector) UPSilon {
	var u UPSilon
	if res, err := c.Run(ctx, "pgrep", "-a", "-f", "-i", "upsilon|rupsd"); err == nil && res.OK() {
		u.Procs = parsePgrep(res.Stdout)
	}
	u.Running = len(u.Procs) > 0
	u.Autostart = findAutostart(c)
	for _, d := range upsilonDirs {
		if c.Exists(d) {
			u.Paths = append(u.Paths, d)
		}
	}
	u.Port, u.PortFrom = upsilonPort(c, u.Paths)
	u.Present = u.Running || len(u.Autostart) > 0 || len(u.Paths) > 0
	return u
}

// parsePgrep — «PID командная строка» по строке (pgrep -a); сам pgrep и
// командные оболочки с grep — не UPSilon.
func parsePgrep(out string) []UPSilonProc {
	var list []UPSilonProc
	for _, line := range strings.Split(out, "\n") {
		pidS, cmd, ok := strings.Cut(strings.TrimSpace(line), " ")
		pid, err := strconv.Atoi(pidS)
		if !ok || err != nil || pid <= 1 {
			continue
		}
		cmd = strings.TrimSpace(cmd)
		first := path.Base(strings.Fields(cmd)[0])
		if first == "pgrep" || first == "grep" || strings.Contains(cmd, "pgrep ") {
			continue
		}
		if len(cmd) > 200 {
			cmd = cmd[:200]
		}
		list = append(list, UPSilonProc{PID: pid, Cmd: cmd})
	}
	return list
}

// findAutostart — службы, init.d, rc.local и cron, где упоминается UPSilon.
func findAutostart(c collect.Collector) []Autostart {
	var out []Autostart
	seen := map[string]bool{}
	add := func(a Autostart) {
		if !seen[a.Path] {
			seen[a.Path] = true
			out = append(out, a)
		}
	}
	for _, pat := range []string{"/etc/systemd/system/*.service", "/lib/systemd/system/*.service", "/usr/lib/systemd/system/*.service"} {
		files, _ := c.Glob(pat)
		for _, f := range files {
			if upsilonRe.MatchString(path.Base(f)) || fileMentions(c, f) != "" {
				add(Autostart{Path: f, Kind: "systemd"})
			}
		}
	}
	files, _ := c.Glob("/etc/init.d/*")
	for _, f := range files {
		if upsilonRe.MatchString(path.Base(f)) || fileMentions(c, f) != "" {
			add(Autostart{Path: f, Kind: "initd"})
		}
	}
	for _, f := range []string{"/etc/rc.local", "/etc/rc.d/rc.local"} {
		if line := fileMentions(c, f); line != "" {
			add(Autostart{Path: f, Kind: "rclocal", Line: line})
		}
	}
	cron, _ := c.Glob("/etc/cron.d/*")
	for _, f := range append([]string{"/etc/crontab"}, cron...) {
		if line := fileMentions(c, f); line != "" {
			add(Autostart{Path: f, Kind: "cron", Line: line})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// fileMentions — первая незакомментированная строка файла с UPSilon.
func fileMentions(c collect.Collector, p string) string {
	raw, err := c.ReadFile(p)
	if err != nil || len(raw) > 256<<10 {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") && upsilonRe.MatchString(t) {
			return t
		}
	}
	return ""
}

var (
	devPortRe = regexp.MustCompile(`/dev/tty(S|USB|ACM)[0-9]{1,3}\b`)
	comPortRe = regexp.MustCompile(`(?i)\bCOM([1-9])\b`)
)

// upsilonPort — порт ИБП из настроек UPSilon: первый /dev/tty… в
// текстовых файлах его каталогов, иначе COMn (Windows-запись) → ttyS(n-1).
func upsilonPort(c collect.Collector, dirs []string) (string, string) {
	var com, comFrom string
	for _, d := range dirs {
		entries, err := c.ListDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir || e.Size > 64<<10 {
				continue
			}
			raw, err := c.ReadFile(e.Path)
			if err != nil || !isText(raw) {
				continue
			}
			if m := devPortRe.FindString(string(raw)); m != "" {
				return m, e.Path
			}
			if m := comPortRe.FindStringSubmatch(string(raw)); m != nil && com == "" {
				n, _ := strconv.Atoi(m[1])
				com, comFrom = "/dev/ttyS"+strconv.Itoa(n-1), e.Path
			}
		}
	}
	return com, comFrom
}

func isText(b []byte) bool {
	for _, x := range b {
		if x == 0 {
			return false
		}
	}
	return true
}

// Adapter — переходник USB-COM и его порт.
type Adapter struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Chip    string `json:"chip"`
	Port    string `json:"port,omitempty"`
}

// knownAdapters — микросхемы переходников USB-COM.
var knownAdapters = []struct{ vendor, product, chip string }{
	{"067b", "2303", "Prolific PL2303"},
	{"067b", "23a3", "Prolific PL2303"},
	{"1a86", "7523", "WCH CH340"},
	{"1a86", "5523", "WCH CH341"},
	{"0403", "6001", "FTDI FT232"},
	{"0403", "6015", "FTDI FT230X"},
	{"10c4", "ea60", "Silicon Labs CP210x"},
}

// detectAdapters — переходники USB-COM и порт, который им дало ядро: он
// в каталоге интерфейса внутри каталога устройства
// (/sys/bus/usb/devices/1-1.2/1-1.2:1.0/ttyUSB0).
func detectAdapters(c collect.Collector) []Adapter {
	out := []Adapter{}
	devs, _ := c.Glob("/sys/bus/usb/devices/*/idVendor")
	sort.Strings(devs)
	for _, vf := range devs {
		dir := path.Dir(vf)
		vendor := strings.ToLower(readTrim(c, vf))
		product := strings.ToLower(readTrim(c, dir+"/idProduct"))
		for _, k := range knownAdapters {
			if vendor != k.vendor || product != k.product {
				continue
			}
			a := Adapter{Vendor: vendor, Product: product, Chip: k.chip}
			for _, pat := range []string{dir + "/*/ttyUSB*", dir + "/*/tty/ttyUSB*", dir + "/*/tty/ttyACM*"} {
				if m, _ := c.Glob(pat); len(m) > 0 {
					a.Port = "/dev/" + path.Base(m[0])
					break
				}
			}
			out = append(out, a)
			break
		}
	}
	return out
}

// Recommendation — драйвер и порт, с которых начинать.
type Recommendation struct {
	Driver string `json:"driver"`
	Port   string `json:"port"`
	// Reason — upsilon (порт из его настроек), adapter, usb, serial.
	Reason string `json:"reason"`
}

func recommend(d Detection) Recommendation {
	if d.UPSilon.Port != "" {
		return Recommendation{Driver: "nutdrv_qx", Port: d.UPSilon.Port, Reason: "upsilon"}
	}
	for _, a := range d.Adapters {
		if a.Port != "" {
			return Recommendation{Driver: "nutdrv_qx", Port: a.Port, Reason: "adapter"}
		}
	}
	if len(d.USB) > 0 {
		return Recommendation{Driver: d.USB[0].Driver, Port: "auto", Reason: "usb"}
	}
	for _, p := range d.Serial {
		if strings.HasPrefix(p, "/dev/ttyUSB") {
			return Recommendation{Driver: "nutdrv_qx", Port: p, Reason: "serial"}
		}
	}
	if len(d.Serial) > 0 {
		return Recommendation{Driver: "nutdrv_qx", Port: d.Serial[0], Reason: "serial"}
	}
	return Recommendation{Driver: "nutdrv_qx", Port: "auto", Reason: ""}
}
