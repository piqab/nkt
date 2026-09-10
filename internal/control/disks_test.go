package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/collect"
)

// Вывод снят с настоящей машины: именно на нём разбор и должен работать,
// а не на придуманном ровном примере.
const dfSample = `Filesystem     Type          1-blocks          Used     Available Capacity Mounted on
none           overlay     4060954624             0    4060954624       0% /usr/lib/modules/6.18.33.2-microsoft-standard-WSL2
none           tmpfs       4060954624          4096    4060950528       1% /mnt/wsl
drivers        9p       2046865436672 1616369377280  430496059392      79% /usr/lib/wsl/drivers
/dev/sdd       ext4     1081101176832    9866629120 1016242192384       1% /
rootfs         rootfs      4055056384       2838528    4052217856       1% /init
`

func TestParseDF(t *testing.T) {
	list := parseDF(dfSample)
	if len(list) != 5 {
		t.Fatalf("разобрано %d строк, ожидалось 5: %+v", len(list), list)
	}

	var root *Filesystem
	for i := range list {
		if list[i].MountPoint == "/" {
			root = &list[i]
		}
	}
	if root == nil {
		t.Fatal("корневая файловая система не найдена")
	}
	if root.Device != "/dev/sdd" || root.Type != "ext4" {
		t.Errorf("корень разобран как %+v", root)
	}
	if root.Size != 1081101176832 || root.Used != 9866629120 {
		t.Errorf("размеры корня: %d / %d", root.Size, root.Used)
	}
	if root.Pseudo {
		t.Error("ext4 помечена псевдофайловой системой")
	}
	// Занятость считается от «занято + доступно», а не от общего размера:
	// на ext4 часть места зарезервирована под root и в df не видна как
	// доступная.
	if root.UsePercent < 0.9 || root.UsePercent > 1.1 {
		t.Errorf("занятость корня %.2f%%, ожидалось около 1%%", root.UsePercent)
	}

	// Всё, что живёт в памяти или поверх другого, помечается — иначе
	// список настоящих разделов тонет среди двух десятков tmpfs.
	pseudo := map[string]bool{}
	for _, f := range list {
		pseudo[f.Type] = f.Pseudo
	}
	for _, kind := range []string{"overlay", "tmpfs", "rootfs", "9p"} {
		if !pseudo[kind] {
			t.Errorf("%s не помечена как псевдофайловая система", kind)
		}
	}
}

// Точка монтирования может содержать пробелы (внешний диск с именем тома
// из проводника) — она идёт последним полем и должна склеиваться обратно.
func TestParseDFMountPointWithSpaces(t *testing.T) {
	out := "Filesystem Type 1-blocks Used Available Capacity Mounted on\n" +
		"/dev/sdb1 vfat 1000 100 900 10% /media/alex/My Backup Disk\n"
	list := parseDF(out)
	if len(list) != 1 || list[0].MountPoint != "/media/alex/My Backup Disk" {
		t.Fatalf("разобрано %+v", list)
	}
}

// lsblk отдаёт половину полей как null — у виртуального диска нет
// транспорта, у раздела без метки нет label. Строгий разбор в строку на
// этом ломается.
const lsblkSample = `{
   "blockdevices": [
      {"name":"sdc","path":"/dev/sdc","type":"disk","size":2147487744,"model":"Virtual Disk",
       "serial":"6002248","tran":null,"fstype":"swap","label":null,"mountpoints":["[SWAP]"],"rota":true},
      {"name":"nvme0n1","path":"/dev/nvme0n1","type":"disk","size":512110190592,"model":"SAMSUNG",
       "serial":"S4EVNF0","tran":"nvme","fstype":null,"label":null,"mountpoints":[null],"rota":false,
       "children":[
          {"name":"nvme0n1p1","path":"/dev/nvme0n1p1","type":"part","size":536870912,"model":null,
           "serial":null,"tran":null,"fstype":"vfat","label":"EFI","mountpoints":["/boot/efi"],"rota":false},
          {"name":"nvme0n1p2","path":"/dev/nvme0n1p2","type":"part","size":511572901888,"model":null,
           "serial":null,"tran":null,"fstype":"ext4","label":null,"mountpoints":["/"],"rota":false}
       ]}
   ]
}`

func TestParseLsblk(t *testing.T) {
	devices, err := parseLsblk(lsblkSample)
	if err != nil {
		t.Fatalf("parseLsblk: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("устройств %d, ожидалось 2", len(devices))
	}

	swap := devices[0]
	if swap.Transport != "" || swap.Label != "" {
		t.Errorf("null-поля должны стать пустыми строками: %+v", swap)
	}
	if len(swap.MountPoints) != 1 || swap.MountPoints[0] != "[SWAP]" {
		t.Errorf("точки монтирования: %v", swap.MountPoints)
	}

	nvme := devices[1]
	if nvme.Rotational {
		t.Error("SSD помечен вращающимся")
	}
	// mountpoints: [null] — устройство без точек монтирования; пустые
	// значения не должны попадать в список как пустые строки.
	if len(nvme.MountPoints) != 0 {
		t.Errorf("у диска с [null] появились точки монтирования: %v", nvme.MountPoints)
	}
	if len(nvme.Children) != 2 {
		t.Fatalf("разделов %d, ожидалось 2", len(nvme.Children))
	}
	if nvme.Children[0].Label != "EFI" || nvme.Children[0].MountPoints[0] != "/boot/efi" {
		t.Errorf("первый раздел разобран как %+v", nvme.Children[0])
	}
}

func TestParseSwapon(t *testing.T) {
	list := parseSwapon("/dev/sdc partition 2147483648 331776\n/swapfile file 1073741824 0\n")
	if len(list) != 2 {
		t.Fatalf("разобрано %d, ожидалось 2", len(list))
	}
	if list[0].Name != "/dev/sdc" || list[0].Size != 2147483648 || list[0].Used != 331776 {
		t.Errorf("первая область: %+v", list[0])
	}
	if list[1].Type != "file" {
		t.Errorf("вторая область: %+v", list[1])
	}
	// Пустой вывод — подкачки нет; это обычное состояние, не ошибка.
	if got := parseSwapon(""); len(got) != 0 {
		t.Errorf("пустой вывод дал %+v", got)
	}
}

func TestParseDU(t *testing.T) {
	out := "4096\t/var/tmp\n" +
		"1073741824\t/var/lib\n" +
		"52428800\t/var/log\n" +
		"1126170624\t/var\n"
	list := parseDU(out, "/var")

	// Сам каталог из списка убирается: он равен сумме остальных, а не
	// занимает место внутри себя.
	if len(list) != 3 {
		t.Fatalf("записей %d, ожидалось 3: %+v", len(list), list)
	}
	// По убыванию — иначе «что съело место» приходится искать глазами.
	if list[0].Path != "/var/lib" || list[1].Path != "/var/log" || list[2].Path != "/var/tmp" {
		t.Errorf("порядок: %+v", list)
	}
}

// nil-срез уходит в JSON как null, а на той стороне обращение к .length у
// null роняет не раздел, а весь интерфейс: исключение в отрисовке
// размонтирует дерево React. Раздел «Диски» именно так и «падал» на
// машине без подкачки. Тест держит границу: в ответе не должно быть
// null-массивов ни при каких обстоятельствах.
func TestOverviewNeverEmitsNullArrays(t *testing.T) {
	// Пустой Overview — то, что получится, если ни одна команда не
	// отработала: ни df, ни lsblk, ни swapon.
	raw, err := json.Marshal(Overview{
		Filesystems: []Filesystem{},
		Devices:     []BlockDevice{},
		Swap:        []Swap{},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, field := range []string{`"filesystems":null`, `"devices":null`, `"swap":null`} {
		if strings.Contains(string(raw), field) {
			t.Errorf("в ответе есть %s — интерфейс упадёт на .length", field)
		}
	}

	// И то же самое для настоящего сборщика на хосте без единой нужной
	// команды: коллектор фикстур ничего из df/lsblk/swapon не знает.
	m := NewDiskManager(collect.NewFixtures(t.TempDir()))
	out := m.Overview(context.Background())
	raw, err = json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(raw), ":null") {
		t.Errorf("сборщик отдал null-массив: %s", raw)
	}
}
