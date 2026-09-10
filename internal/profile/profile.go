// Package profile описывает желаемое состояние хоста и сравнивает его с
// действительным.
//
// Это не «ещё один Terraform»: ни графа зависимостей, ни провайдеров, ни
// собственного языка — плоский список ресурсов, каждый из которых nkt и
// так умеет менять сам. Ценность не столько в применении, сколько в
// вопросе «а что на сервере разошлось с задуманным»: ответ на него
// собирается из уже имеющейся инвентаризации.
//
// Два решения, которые важнее остальных:
//
//   - Профиль аддитивный. Он говорит, что должно быть, и молчит обо всём
//     остальном: пакет, которого нет в списке, не удаляется. Иначе первый
//     же неполный профиль выключил бы половину сервера.
//   - Неуказанное поле не значит «выключить». Отсюда указатели в Service:
//     nil — «не моё дело», false — «должно быть выключено».
package profile

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Version — версия формата. Растёт, только если старый профиль перестанет
// читаться новым кодом.
const Version = 1

// maxFileBytes — потолок содержимого одного файла в профиле. Профиль
// правят в браузере и хранят в базе; мегабайтные файлы туда не относятся.
const maxFileBytes = 512 << 10

// Profile — желаемое состояние одного хоста или группы.
type Profile struct {
	Version int    `json:"version" yaml:"version"`
	Name    string `json:"name" yaml:"name"`
	// Packages — пакеты, которые должны быть установлены (apt).
	Packages []string `json:"packages,omitempty" yaml:"packages,omitempty"`
	// Services — состояние служб systemd по имени.
	Services map[string]Service `json:"services,omitempty" yaml:"services,omitempty"`
	// Files — файлы конфигурации с содержимым целиком. Хранить хеш вместо
	// содержимого нельзя: тогда расхождение видно, а привести в
	// соответствие нечем.
	Files []File `json:"files,omitempty" yaml:"files,omitempty"`
	// Firewall — порты, которые должны быть открыты.
	Firewall *Firewall `json:"firewall,omitempty" yaml:"firewall,omitempty"`
	// Users — системные учётные записи и их ключи.
	Users []User `json:"users,omitempty" yaml:"users,omitempty"`
	// System — имя машины и часовой пояс.
	System *System `json:"system,omitempty" yaml:"system,omitempty"`
}

// Service — желаемое состояние службы. Указатели, а не bool: «не указано»
// и «должно быть выключено» — разные требования, и путать их нельзя.
type Service struct {
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Active  *bool `json:"active,omitempty" yaml:"active,omitempty"`
}

// File — файл конфигурации.
type File struct {
	Path    string `json:"path" yaml:"path"`
	Content string `json:"content" yaml:"content"`
	// Mode — права в восьмеричном виде («0644»). Пусто — не трогать
	// существующие, а новый файл создать с 0644.
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
}

// Firewall — открытые порты. Backend не задаётся: чем именно закрыт хост
// (ufw или firewalld), решает сам хост, а профиль говорит о желаемом
// доступе.
type Firewall struct {
	Allow []Port `json:"allow,omitempty" yaml:"allow,omitempty"`
}

// Port — один разрешённый порт.
type Port struct {
	Port  int    `json:"port" yaml:"port"`
	Proto string `json:"proto,omitempty" yaml:"proto,omitempty"` // tcp (по умолчанию) | udp
	// From — источник (адрес или подсеть). Пусто — отовсюду.
	From string `json:"from,omitempty" yaml:"from,omitempty"`
}

// User — системная учётная запись.
type User struct {
	Name string `json:"name" yaml:"name"`
	Sudo *bool  `json:"sudo,omitempty" yaml:"sudo,omitempty"`
	// Keys — публичные ключи, которые должны лежать в authorized_keys.
	// Лишние ключи не удаляются: профиль аддитивный, а чужой ключ мог
	// положить туда человек, а не забытый профиль.
	Keys []string `json:"keys,omitempty" yaml:"keys,omitempty"`
}

// System — общесистемные настройки.
type System struct {
	Hostname string `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	Timezone string `json:"timezone,omitempty" yaml:"timezone,omitempty"`
}

var (
	packageRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
	serviceRe  = regexp.MustCompile(`^[A-Za-z0-9@._-]+$`)
	userRe     = regexp.MustCompile(`^[a-z_][a-z0-9_-]*\$?$`)
	hostnameRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
)

// Parse читает профиль из YAML.
func Parse(raw []byte) (Profile, error) {
	var p Profile
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true) // опечатка в имени поля — ошибка, а не тихо забытая настройка
	if err := dec.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("разбор профиля: %w", err)
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Marshal отдаёт профиль в YAML — для выгрузки в git.
func (p Profile) Marshal() ([]byte, error) { return yaml.Marshal(p) }

// Validate проверяет то, что уйдёт в команды на настоящем сервере: имена
// пакетов, служб и учёток попадают в командную строку, а пути файлов — в
// запись на диск.
func (p Profile) Validate() error {
	if p.Version != 0 && p.Version != Version {
		return fmt.Errorf("версия профиля %d, поддерживается %d", p.Version, Version)
	}
	for _, pkg := range p.Packages {
		if !packageRe.MatchString(pkg) {
			return fmt.Errorf("некорректное имя пакета %q", pkg)
		}
	}
	for name := range p.Services {
		if !serviceRe.MatchString(name) {
			return fmt.Errorf("некорректное имя службы %q", name)
		}
	}
	seenPath := map[string]bool{}
	for _, f := range p.Files {
		switch {
		case !strings.HasPrefix(f.Path, "/"):
			return fmt.Errorf("путь файла должен быть абсолютным: %q", f.Path)
		case strings.Contains(f.Path, ".."):
			return fmt.Errorf("путь файла не может содержать «..»: %q", f.Path)
		case seenPath[f.Path]:
			return fmt.Errorf("файл %q указан дважды", f.Path)
		case len(f.Content) > maxFileBytes:
			return fmt.Errorf("файл %q длиннее %d КиБ", f.Path, maxFileBytes>>10)
		}
		if f.Mode != "" && !regexp.MustCompile(`^0?[0-7]{3}$`).MatchString(f.Mode) {
			return fmt.Errorf("права файла %q должны быть восьмеричными, получено %q", f.Path, f.Mode)
		}
		seenPath[f.Path] = true
	}
	if p.Firewall != nil {
		for _, port := range p.Firewall.Allow {
			if port.Port < 1 || port.Port > 65535 {
				return fmt.Errorf("порт вне диапазона: %d", port.Port)
			}
			if port.Proto != "" && port.Proto != "tcp" && port.Proto != "udp" {
				return fmt.Errorf("протокол должен быть tcp или udp, получено %q", port.Proto)
			}
		}
	}
	for _, u := range p.Users {
		if !userRe.MatchString(u.Name) {
			return fmt.Errorf("некорректное имя учётной записи %q", u.Name)
		}
		for _, k := range u.Keys {
			if !strings.HasPrefix(k, "ssh-") && !strings.HasPrefix(k, "ecdsa-") && !strings.HasPrefix(k, "sk-") {
				return fmt.Errorf("ключ учётной записи %q не похож на публичный ключ SSH", u.Name)
			}
		}
	}
	if p.System != nil && p.System.Hostname != "" && !hostnameRe.MatchString(p.System.Hostname) {
		return fmt.Errorf("некорректное имя машины %q", p.System.Hostname)
	}
	return nil
}

// Normalize приводит профиль к виду, в котором его сравнивают и хранят:
// без пустых значений, с предсказуемым порядком.
func (p Profile) Normalize() Profile {
	out := p
	out.Version = Version

	out.Packages = uniqueSorted(p.Packages)
	if len(out.Packages) == 0 {
		out.Packages = nil
	}
	if len(p.Services) == 0 {
		out.Services = nil
	}
	out.Files = append([]File(nil), p.Files...)
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	if len(out.Files) == 0 {
		out.Files = nil
	}
	if p.Firewall != nil {
		allow := append([]Port(nil), p.Firewall.Allow...)
		for i := range allow {
			if allow[i].Proto == "" {
				allow[i].Proto = "tcp"
			}
		}
		sort.Slice(allow, func(i, j int) bool {
			if allow[i].Port != allow[j].Port {
				return allow[i].Port < allow[j].Port
			}
			return allow[i].Proto < allow[j].Proto
		})
		out.Firewall = &Firewall{Allow: allow}
	}
	out.Users = append([]User(nil), p.Users...)
	sort.Slice(out.Users, func(i, j int) bool { return out.Users[i].Name < out.Users[j].Name })
	if len(out.Users) == 0 {
		out.Users = nil
	}
	return out
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// Bool — помощник для указателей в профиле, собранном в коде или тестах.
func Bool(v bool) *bool { return &v }
