// Package statediff сравнивает два снимка состояния хоста и говорит, что
// между ними изменилось.
//
// Снимки nkt хранит и так: каждое сканирование, отличающееся от
// предыдущего, ложится в таблицу snapshots. Не хватало ответа на вопрос,
// ради которого их и хранят, — «что поменялось с прошлого раза»: был
// открыт порт, а стал закрыт; служба работала, а теперь остановлена;
// появился файл конфигурации, которого не было.
//
// Сравнивается не сырой JSON, а отобранный набор фактов. Сырой диф был бы
// бесполезен: в снимке лежит и память процесса, и «Up 8 days», и время
// сканирования — такой «диф» показывал бы изменения каждую минуту и
// прятал среди них единственное настоящее. Здесь перечислено ровно то,
// изменение чего означает изменение состояния сервера.
package statediff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/model"
)

// Виды сравниваемых фактов.
const (
	KindService   = "service"
	KindPort      = "port"
	KindFirewall  = "firewall"
	KindContainer = "container"
	KindVM        = "vm"
	KindFile      = "file"
	KindCert      = "cert"
	KindInterface = "interface"
	KindPackage   = "package"
)

// Что произошло с фактом.
const (
	// Appeared — факта не было, а теперь есть: новая служба, новый
	// открытый порт, новый файл.
	Appeared = "appeared"
	// Disappeared — был и пропал.
	Disappeared = "disappeared"
	// Changed — остался, но в другом состоянии.
	Changed = "changed"
)

// Fact — один сравниваемый факт: что это (Kind), что именно (Key) и в
// каком оно состоянии (Value).
//
// Value — короткая техническая запись (systemd'шные «active/enabled»,
// состояние домена, начало хеша файла), а не фраза: её показывают как
// есть на любом языке интерфейса, и переводить там нечего.
type Fact struct {
	Kind  string
	Key   string
	Value string
}

// Change — одно расхождение между снимками.
type Change struct {
	Kind   string `json:"kind"`
	Key    string `json:"key"`
	Action string `json:"action"`
	Was    string `json:"was,omitempty"`
	Now    string `json:"now,omitempty"`
}

// Facts выбирает из снимка то, по чему сравнивают состояние.
func Facts(s model.Snapshot) []Fact {
	var out []Fact
	add := func(kind, key, value string) {
		if key == "" {
			return
		}
		out = append(out, Fact{Kind: kind, Key: key, Value: value})
	}

	for _, svc := range s.Services {
		if !svc.Installed {
			// Неустановленная служба — не состояние сервера, а пустое
			// место в списке известных nkt служб.
			continue
		}
		add(KindService, svc.Name, strings.TrimSpace(fmt.Sprintf("%s/%s", svc.ActiveState, svc.Enabled)))
	}

	for _, l := range s.Listeners {
		// Ключ — что именно слушают, а не кто: процесс могут заменить
		// (nginx на caddy), и это изменение, а не новый порт.
		add(KindPort, fmt.Sprintf("%s/%d", l.Protocol, l.Port), l.Process)
	}

	for _, r := range s.Firewall.Rules {
		add(KindFirewall, firewallKey(r), r.Action)
	}

	for _, c := range s.Container {
		add(KindContainer, c.Name, fmt.Sprintf("%s %s", c.Image, c.State))
	}
	for _, c := range s.Podman {
		add(KindContainer, "podman:"+c.Name, fmt.Sprintf("%s %s", c.Image, c.State))
	}
	for _, i := range s.LXD {
		add(KindContainer, "lxd:"+i.Name, i.Status)
	}
	for _, vm := range s.VMs {
		add(KindVM, vm.Name, vm.State)
	}

	for _, f := range s.Files {
		// Хеш, а не время правки: файл, переписанный тем же содержимым,
		// состояния сервера не меняет.
		add(KindFile, f.Path, shortHash(f.SHA256))
	}

	for _, c := range s.Certs {
		key := c.Path
		if key == "" {
			key = c.Subject
		}
		add(KindCert, key, c.NotAfter.Format("2006-01-02"))
	}

	for _, iface := range s.Interfaces {
		add(KindInterface, iface.Name, strings.Join(iface.Addresses, " "))
	}

	for _, p := range s.Packages.Packages {
		add(KindPackage, p.Name, p.NewVersion)
	}

	return out
}

// firewallKey описывает правило так, чтобы его можно было узнать в
// соседнем снимке. Не ID и не порядковый номер: и то, и другое меняется
// от простой перестановки правил, а правило остаётся тем же.
func firewallKey(r model.FirewallRule) string {
	parts := []string{r.Backend, r.Chain}
	if r.Protocol != "" {
		parts = append(parts, r.Protocol)
	}
	if r.PortSpec != "" {
		parts = append(parts, r.PortSpec)
	}
	if r.Source != "" {
		parts = append(parts, "from "+r.Source)
	}
	if r.Destination != "" {
		parts = append(parts, "to "+r.Destination)
	}
	if r.Zone != "" {
		parts = append(parts, "zone "+r.Zone)
	}
	return strings.Join(parts, " ")
}

func shortHash(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

// Diff сравнивает два снимка.
//
// Порядок ответа устойчив: сначала по виду, потом по ключу. Список
// изменений читают глазами, и прыгающий порядок мешал бы сверять его с
// прошлым разом.
func Diff(prev, cur model.Snapshot) []Change {
	was := index(Facts(prev))
	now := index(Facts(cur))

	var out []Change
	for key, value := range now {
		old, ok := was[key]
		switch {
		case !ok:
			out = append(out, Change{Kind: key.kind, Key: key.key, Action: Appeared, Now: value})
		case old != value:
			out = append(out, Change{Kind: key.kind, Key: key.key, Action: Changed, Was: old, Now: value})
		}
	}
	for key, value := range was {
		if _, ok := now[key]; !ok {
			out = append(out, Change{Kind: key.kind, Key: key.key, Action: Disappeared, Was: value})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Key < out[j].Key
	})
	return out
}

type factKey struct{ kind, key string }

func index(facts []Fact) map[factKey]string {
	out := make(map[factKey]string, len(facts))
	for _, f := range facts {
		out[factKey{f.Kind, f.Key}] = f.Value
	}
	return out
}
