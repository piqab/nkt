package control

import "testing"

// Вывод lscpu -J снят с этой машины: плоский список пар, где имя поля
// включает двоеточие.
func TestParseLscpu(t *testing.T) {
	out := `{"lscpu":[
	  {"field":"Architecture:","data":"x86_64"},
	  {"field":"CPU(s):","data":"16"},
	  {"field":"Vendor ID:","data":"AuthenticAMD"},
	  {"field":"Model name:","data":"AMD Ryzen AI Z2 Extreme"},
	  {"field":"Thread(s) per core:","data":"2"},
	  {"field":"Core(s) per socket:","data":"8"},
	  {"field":"Socket(s):","data":"1"},
	  {"field":"CPU max MHz:","data":"5100.0000"}
	]}`
	info := parseLscpu(out)
	if info.Model != "AMD Ryzen AI Z2 Extreme" || info.Vendor != "AuthenticAMD" || info.Arch != "x86_64" {
		t.Errorf("разбор дал %+v", info)
	}
	if info.CPUs != 16 || info.Cores != 8 || info.Threads != 2 || info.Sockets != 1 {
		t.Errorf("счётчики: %+v", info)
	}
	if info.MaxMHz < 5099 || info.MaxMHz > 5101 {
		t.Errorf("частота %v", info.MaxMHz)
	}
	// Мусор вместо JSON не должен ронять сбор — просто пустая карточка.
	if got := parseLscpu("не json"); got.CPUs != 0 {
		t.Errorf("мусор дал %+v", got)
	}
}

func TestParseMeminfo(t *testing.T) {
	info := parseMeminfo("MemTotal:        7931552 kB\nMemFree: 100 kB\nMemAvailable:    5371180 kB\nSwapTotal:       2097152 kB\n")
	// В /proc/meminfo килобайты; наружу отдаются байты, чтобы
	// форматирование было общим со всем остальным.
	if info.TotalBytes != 7931552*1024 || info.AvailableBytes != 5371180*1024 || info.SwapTotalBytes != 2097152*1024 {
		t.Errorf("разбор дал %+v", info)
	}
}

// sensors -j даёт три уровня вложенности, и имена полей у каждого чипа
// свои: temp1_input у одного, Core 0 у другого. Разбор идёт по суффиксу.
func TestParseSensors(t *testing.T) {
	out := `{
	  "k10temp-pci-00c3": {
	    "Tctl": {"temp1_input": 45.5, "temp1_max": 95.0, "temp1_crit": 100.0},
	    "Adapter": "PCI adapter"
	  },
	  "nvme-pci-0100": {
	    "Composite": {"temp1_input": 38.85, "temp1_crit": 84.85}
	  }
	}`
	list := parseSensors(out)
	if len(list) != 2 {
		t.Fatalf("датчиков %d, ожидалось 2: %+v", len(list), list)
	}
	byLabel := map[string]SensorValue{}
	for _, v := range list {
		byLabel[v.Label] = v
	}
	if v := byLabel["Tctl"]; v.Celsius != 45.5 || v.Warn != 95 || v.Crit != 100 {
		t.Errorf("Tctl разобран как %+v", v)
	}
	// "Adapter": "PCI adapter" — строка, а не объект с температурой:
	// такие поля должны отсеиваться, иначе в списке появится пустой датчик.
	if _, ok := byLabel["Adapter"]; ok {
		t.Error("строковое поле принято за датчик")
	}
}

func TestParseKeyValue(t *testing.T) {
	kv := parseKeyValue("Timezone=Asia/Qyzylorda\nNTP=no\nNTPSynchronized=yes\n# комментарий\n")
	if kv["Timezone"] != "Asia/Qyzylorda" || kv["NTP"] != "no" || kv["NTPSynchronized"] != "yes" {
		t.Errorf("разбор дал %+v", kv)
	}
	if _, ok := kv["# комментарий"]; ok {
		t.Error("комментарий принят за пару")
	}
}

// nmcli -t разделяет поля двоеточием, а двоеточие внутри значения
// экранирует обратным слешем — имя соединения вполне может его
// содержать, и наивный Split разорвал бы строку не там.
func TestSplitNM(t *testing.T) {
	got := splitNM(`Wired connection 1:uuid-1:802-3-ethernet:eth0:yes`)
	if len(got) != 5 || got[0] != "Wired connection 1" || got[4] != "yes" {
		t.Fatalf("разбор дал %q", got)
	}
	escaped := splitNM(`Home\: Wi-Fi:uuid-2:wifi:wlan0:no`)
	if len(escaped) != 5 || escaped[0] != "Home: Wi-Fi" {
		t.Errorf("экранированное двоеточие разобрано как %q", escaped)
	}
}

func TestParseNMConnections(t *testing.T) {
	out := "Wired connection 1:11111111-1111:802-3-ethernet:eth0:yes\n" +
		"Home Wi-Fi:22222222-2222:802-11-wireless:--:no\n"
	list := parseNMConnections(out)
	if len(list) != 2 {
		t.Fatalf("соединений %d", len(list))
	}
	if !list[0].Active || list[0].Device != "eth0" {
		t.Errorf("активное соединение: %+v", list[0])
	}
	// Неактивное соединение показывает «--» вместо устройства — в ответе
	// это должно стать пустой строкой, а не двумя дефисами на экране.
	if list[1].Active || list[1].Device != "" {
		t.Errorf("неактивное соединение: %+v", list[1])
	}
}

func TestParseNMWiFi(t *testing.T) {
	out := "*:HomeNet:78:WPA2\n :Guest:45:WPA2\n :" + ":30:--\n"
	list := parseNMWiFi(out)
	if len(list) != 2 {
		t.Fatalf("сетей %d, ожидалось 2 (скрытая без имени отбрасывается): %+v", len(list), list)
	}
	if !list[0].InUse || list[0].SSID != "HomeNet" || list[0].Signal != 78 {
		t.Errorf("текущая сеть: %+v", list[0])
	}
	if list[1].InUse {
		t.Errorf("вторая сеть помечена активной: %+v", list[1])
	}
}

// Табличный вывод snap list и табулированный flatpak list — разные
// форматы, и общего разбора у них нет.
func TestParseSnapAndFlatpak(t *testing.T) {
	snaps := parseSnapList("Name  Version  Rev  Tracking  Publisher  Notes\n" +
		"core22  20240111  1122  latest/stable  canonical**  base\n" +
		"firefox  122.0-2  3600  latest/stable  mozilla**  -\n")
	if len(snaps) != 2 {
		t.Fatalf("snap'ов %d: %+v", len(snaps), snaps)
	}
	if snaps[1].Name != "firefox" || snaps[1].Version != "122.0-2" || snaps[1].Channel != "latest/stable" {
		t.Errorf("firefox разобран как %+v", snaps[1])
	}
	if snaps[0].Kind != "snap" {
		t.Errorf("вид пакета: %q", snaps[0].Kind)
	}

	flats := parseFlatpakList("org.gimp.GIMP\tGNU Image Manipulation Program\t2.10.36\tstable\tflathub\n")
	if len(flats) != 1 {
		t.Fatalf("flatpak'ов %d", len(flats))
	}
	// Удаление идёт по идентификатору, а не по человеческому имени —
	// поэтому оба поля хранятся отдельно.
	if flats[0].ID != "org.gimp.GIMP" || flats[0].Name != "GNU Image Manipulation Program" {
		t.Errorf("разбор дал %+v", flats[0])
	}
}
