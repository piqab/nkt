package profile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/model"
)

// fakeReader — состояние хоста, заданное прямо в тесте. Сравнение так
// проверяется без живой машины: именно оно и есть предмет проверки.
type fakeReader struct {
	packages map[string]bool
	services map[string][3]bool // установлена, включена, запущена
	files    map[string]string
	firewall model.FirewallState
	users    map[string]UserState
	hostname string
	timezone string
	fail     map[string]error
}

func (f fakeReader) InstalledPackages(_ context.Context, names []string) (map[string]bool, error) {
	if err := f.fail["packages"]; err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, n := range names {
		out[n] = f.packages[n]
	}
	return out, nil
}

func (f fakeReader) ServiceState(_ context.Context, name string) (bool, bool, bool, error) {
	if err := f.fail["services"]; err != nil {
		return false, false, false, err
	}
	st, ok := f.services[name]
	if !ok {
		return false, false, false, nil
	}
	return st[0], st[1], st[2], nil
}

func (f fakeReader) FileContent(_ context.Context, path string) (string, bool, error) {
	if err := f.fail["files"]; err != nil {
		return "", false, err
	}
	c, ok := f.files[path]
	return c, ok, nil
}

func (f fakeReader) FirewallState(context.Context) (model.FirewallState, error) {
	return f.firewall, f.fail["firewall"]
}

func (f fakeReader) Users(context.Context) (map[string]UserState, error) {
	return f.users, f.fail["users"]
}

func (f fakeReader) System(context.Context) (string, string, error) {
	return f.hostname, f.timezone, f.fail["system"]
}

func actions(p Plan) []string {
	out := make([]string, 0, len(p.Changes))
	for _, c := range p.Changes {
		out = append(out, c.Action+":"+c.Target)
	}
	return out
}

func TestPlanFindsEveryKindOfDrift(t *testing.T) {
	p := Profile{
		Name:     "web",
		Packages: []string{"nginx", "fail2ban"},
		Services: map[string]Service{
			"nginx":    {Enabled: Bool(true), Active: Bool(true)},
			"fail2ban": {Active: Bool(true)},
		},
		Files:    []File{{Path: "/etc/nginx/conf.d/app.conf", Content: "server {}\n"}},
		Firewall: &Firewall{Allow: []Port{{Port: 443}, {Port: 80}}},
		Users:    []User{{Name: "deploy", Sudo: Bool(true), Keys: []string{"ssh-ed25519 AAAAKEY deploy@laptop"}}},
		System:   &System{Hostname: "web-01", Timezone: "Europe/Moscow"},
	}
	host := fakeReader{
		packages: map[string]bool{"nginx": true},
		services: map[string][3]bool{
			"nginx":    {true, false, true},
			"fail2ban": {true, true, false},
		},
		files: map[string]string{"/etc/nginx/conf.d/app.conf": "server { listen 8080; }\n"},
		firewall: model.FirewallState{Rules: []model.FirewallRule{
			{Action: "ACCEPT", Protocol: "tcp", Ports: []int{443}},
		}},
		users:    map[string]UserState{},
		hostname: "localhost",
		timezone: "Europe/Moscow",
	}

	plan := Build(context.Background(), p, host)
	got := strings.Join(actions(plan), "\n")
	want := []string{
		ActionInstallPackage + ":fail2ban", // nginx уже стоит
		ActionEnableService + ":nginx",     // запущена, но не включена
		ActionStartService + ":fail2ban",   // включена, но не запущена
		ActionWriteFile + ":/etc/nginx/conf.d/app.conf",
		ActionAllowPort + ":80/tcp", // 443 уже открыт
		ActionCreateUser + ":deploy",
		ActionGrantSudo + ":deploy",
		ActionAddKey + ":deploy",
		ActionSetHostname + ":web-01", // часовой пояс уже верный
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("в плане нет %q. План:\n%s", w, got)
		}
	}
	if len(plan.Changes) != len(want) {
		t.Errorf("в плане %d пунктов, ожидалось %d:\n%s", len(plan.Changes), len(want), got)
	}
	if len(plan.Unknown) != 0 {
		t.Errorf("неожиданное «не знаю»: %q", plan.Unknown)
	}
}

// Совпавшее состояние даёт пустой план — иначе «применить» предлагалось
// бы вечно.
func TestPlanEmptyWhenEverythingMatches(t *testing.T) {
	p := Profile{
		Packages: []string{"nginx"},
		Services: map[string]Service{"nginx": {Enabled: Bool(true), Active: Bool(true)}},
		Files:    []File{{Path: "/etc/app.conf", Content: "x\n"}},
		Firewall: &Firewall{Allow: []Port{{Port: 443, Proto: "tcp"}}},
		Users:    []User{{Name: "deploy", Keys: []string{"ssh-ed25519 AAAAKEY другой-комментарий"}}},
		System:   &System{Hostname: "web-01"},
	}
	host := fakeReader{
		packages: map[string]bool{"nginx": true},
		services: map[string][3]bool{"nginx": {true, true, true}},
		files:    map[string]string{"/etc/app.conf": "x\n"},
		firewall: model.FirewallState{Rules: []model.FirewallRule{
			{Action: "ALLOW", Protocol: "tcp", Ports: []int{443}},
		}},
		// Тот же ключ с другим комментарием — это тот же ключ.
		users:    map[string]UserState{"deploy": {Keys: []string{"ssh-ed25519 AAAAKEY ноутбук"}}},
		hostname: "web-01",
	}
	plan := Build(context.Background(), p, host)
	if !plan.Empty() {
		t.Errorf("план не пуст: %q", actions(plan))
	}
}

// Неуказанное поле — не требование. Профиль, который просит службу быть
// включённой, ничего не говорит о том, запущена ли она.
func TestPlanIgnoresUnsetFields(t *testing.T) {
	p := Profile{Services: map[string]Service{"nginx": {Enabled: Bool(true)}}}
	host := fakeReader{services: map[string][3]bool{"nginx": {true, true, false}}}
	if plan := Build(context.Background(), p, host); !plan.Empty() {
		t.Errorf("остановленная служба попала в план, хотя профиль про запуск молчит: %q", actions(plan))
	}
}

// Ошибка чтения одного ресурса не должна прятать расхождения в остальных
// — иначе недоступный dpkg делает план бесполезным целиком.
func TestPlanKeepsGoingAfterReadError(t *testing.T) {
	p := Profile{
		Packages: []string{"nginx"},
		Files:    []File{{Path: "/etc/app.conf", Content: "x\n"}},
	}
	host := fakeReader{
		fail:  map[string]error{"packages": errors.New("dpkg молчит")},
		files: map[string]string{},
	}
	plan := Build(context.Background(), p, host)
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ActionWriteFile {
		t.Errorf("расхождение по файлам потеряно: %q", actions(plan))
	}
	if len(plan.Unknown) != 1 || !strings.Contains(plan.Unknown[0], "dpkg молчит") {
		t.Errorf("причина «не знаю» = %q", plan.Unknown)
	}
}

// Служба, которой на хосте нет вовсе, — это «не знаю», а не «надо
// запустить»: ставить пакет наугад по имени юнита нельзя.
func TestPlanReportsMissingServiceAsUnknown(t *testing.T) {
	p := Profile{Services: map[string]Service{"exotic": {Active: Bool(true)}}}
	plan := Build(context.Background(), p, fakeReader{})
	if len(plan.Changes) != 0 {
		t.Errorf("несуществующая служба попала в план: %q", actions(plan))
	}
	if len(plan.Unknown) != 1 {
		t.Errorf("не отмечено как «не знаю»: %+v", plan)
	}
}

// Отзыв sudo профилем не делается: это разрыв доступа, который легко
// получить опечаткой.
func TestPlanNeverRevokesSudo(t *testing.T) {
	p := Profile{Users: []User{{Name: "deploy", Sudo: Bool(false)}}}
	host := fakeReader{users: map[string]UserState{"deploy": {Sudo: true}}}
	plan := Build(context.Background(), p, host)
	if len(plan.Changes) != 0 {
		t.Errorf("отзыв sudo попал в план: %q", actions(plan))
	}
	if len(plan.Unknown) != 1 || !strings.Contains(plan.Unknown[0], "вручную") {
		t.Errorf("оператору не сказано, что делать: %q", plan.Unknown)
	}
}

// Опасные пункты помечаются: остановка sshd и правка его конфига — самый
// быстрый способ потерять доступ к хосту.
func TestPlanMarksRiskyChanges(t *testing.T) {
	p := Profile{
		Services: map[string]Service{"ssh": {Active: Bool(false)}},
		Files:    []File{{Path: "/etc/ssh/sshd_config", Content: "Port 22\n"}},
	}
	host := fakeReader{
		services: map[string][3]bool{"ssh": {true, true, true}},
		files:    map[string]string{"/etc/ssh/sshd_config": "Port 2222\n"},
	}
	plan := Build(context.Background(), p, host)
	for _, c := range plan.Changes {
		if c.Risk == "" {
			t.Errorf("пункт %s:%s не помечен как опасный", c.Action, c.Target)
		}
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("ожидалось два пункта, получено %q", actions(plan))
	}
}

// Состояния в плане — коды, а не готовые фразы: интерфейс бывает и
// английским, а переводить прилетевшую с сервера русскую строку нечем.
func TestPlanUsesStateCodes(t *testing.T) {
	p := Profile{Packages: []string{"nginx"}}
	plan := Build(context.Background(), p, fakeReader{})
	if len(plan.Changes) != 1 {
		t.Fatalf("план = %q", actions(plan))
	}
	c := plan.Changes[0]
	if c.Current != StateMissing || c.Desired != StateInstalled {
		t.Errorf("состояния = %q → %q, ожидались коды", c.Current, c.Desired)
	}
}

// План без расхождений обязан отдавать пустой список, а не null: nil-срез
// уезжает в JSON как null, и «changes.length» на стороне браузера роняет
// страницу целиком. На этом уже спотыкались «Диски».
func TestPlanNeverEmitsNullArrays(t *testing.T) {
	plan := Build(context.Background(), Profile{Name: "пусто"}, fakeReader{})
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(raw), `"changes":null`) {
		t.Errorf("в JSON есть null вместо пустого списка:\n%s", raw)
	}
	var back struct {
		Changes []Change `json:"changes"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Changes == nil {
		t.Error("после разбора changes = nil")
	}
}
