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
	// running — сколько контейнеров стека работает, по пути файла.
	running map[string]int
	fail    map[string]error
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

// composeActions — только виды действий, без целей: путь у всех стеков
// один и тот же, и сравнивать его в каждой проверке незачем.
func composeActions(p Plan) []string {
	out := make([]string, 0, len(p.Changes))
	for _, c := range p.Changes {
		out = append(out, c.Action)
	}
	return out
}

func (f fakeReader) ComposeRunning(_ context.Context, path string) (int, error) {
	if err := f.fail["compose"]; err != nil {
		return 0, err
	}
	return f.running[path], nil
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

// Стек compose — два разных дела: положить описание и поднять по нему
// контейнеры. Оператор должен видеть оба, а не одно «применить».
func TestPlanCompose(t *testing.T) {
	stack := "services:\n  web:\n    image: nginx:1.27\n"
	prof := Profile{Name: "srv", Compose: []Compose{{Name: "shop", Content: stack}}}
	path := "/srv/compose/shop/docker-compose.yml"

	// Ничего нет: записать и поднять.
	plan := Build(context.Background(), prof, fakeReader{})
	if got := composeActions(plan); len(got) != 2 || got[0] != ActionWriteCompose || got[1] != ActionComposeUp {
		t.Fatalf("на пустом хосте = %v", got)
	}
	if plan.Changes[0].Detail != stack {
		t.Errorf("описание стека не попало в план: %q", plan.Changes[0].Detail)
	}

	// Файл тот же, контейнеры работают: расхождений нет.
	same := fakeReader{files: map[string]string{path: stack}, running: map[string]int{path: 2}}
	if plan := Build(context.Background(), prof, same); !plan.Empty() {
		t.Errorf("совпадающий стек дал расхождения: %v", composeActions(plan))
	}

	// Файл тот же, но стек опущен — поднять.
	stopped := fakeReader{files: map[string]string{path: stack}}
	if got := composeActions(Build(context.Background(), prof, stopped)); len(got) != 1 || got[0] != ActionComposeUp {
		t.Errorf("остановленный стек = %v", got)
	}

	// Описание изменилось — переписать и поднять заново, даже если
	// контейнеры работают: сам по себе файл их не трогает.
	old := fakeReader{
		files:   map[string]string{path: "services:\n  web:\n    image: nginx:1.25\n"},
		running: map[string]int{path: 1},
	}
	if got := composeActions(Build(context.Background(), prof, old)); len(got) != 2 ||
		got[0] != ActionWriteCompose || got[1] != ActionComposeUp {
		t.Errorf("устаревшее описание = %v", got)
	}

	// up: false — стек должен быть опущен.
	no := false
	down := Profile{Name: "srv", Compose: []Compose{{Name: "shop", Content: stack, Up: &no}}}
	running := fakeReader{files: map[string]string{path: stack}, running: map[string]int{path: 1}}
	if got := composeActions(Build(context.Background(), down, running)); len(got) != 1 || got[0] != ActionComposeDown {
		t.Errorf("«должен быть опущен» = %v", got)
	}

	// Docker молчит — это «не знаю», а не «ни одного контейнера»: иначе
	// применение полезло бы поднимать работающий стек.
	broken := fakeReader{
		files: map[string]string{path: stack},
		fail:  map[string]error{"compose": errors.New("docker не отвечает")},
	}
	plan = Build(context.Background(), prof, broken)
	for _, c := range plan.Changes {
		if c.Action == ActionComposeUp {
			t.Errorf("молчащий docker понят как «стек не поднят»: %v", composeActions(plan))
		}
	}
	if len(plan.Unknown) == 0 {
		t.Errorf("отказ docker не попал в «не знаю»")
	}
}

// Путь стека берётся из имени, если не задан явно: имя каталога — это имя
// проекта docker, и у каждого стека оно должно быть своим.
func TestComposeFilePath(t *testing.T) {
	if got := (Compose{Name: "shop"}).FilePath(); got != "/srv/compose/shop/docker-compose.yml" {
		t.Errorf("путь по умолчанию = %q", got)
	}
	if got := (Compose{Name: "shop", Path: "/home/op/shop/compose.yml"}).FilePath(); got != "/home/op/shop/compose.yml" {
		t.Errorf("заданный путь потерян: %q", got)
	}
}

func TestComposeValidate(t *testing.T) {
	bad := map[string]Profile{
		"имя с пробелом":   {Name: "p", Compose: []Compose{{Name: "мой стек", Content: "services: {}"}}},
		"пустое описание":  {Name: "p", Compose: []Compose{{Name: "shop", Content: "  "}}},
		"чужое имя файла":  {Name: "p", Compose: []Compose{{Name: "shop", Content: "services: {}", Path: "/srv/compose/shop/stack.yml"}}},
		"путь не абсолютный": {Name: "p", Compose: []Compose{{Name: "shop", Content: "services: {}", Path: "shop/compose.yml"}}},
		"дважды один стек": {Name: "p", Compose: []Compose{
			{Name: "shop", Content: "services: {}"}, {Name: "shop", Content: "services: {}"}}},
	}
	for name, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: принято без ошибки", name)
		}
	}
	ok := Profile{Name: "p", Compose: []Compose{{Name: "shop-1", Content: "services:\n  web:\n    image: nginx\n"}}}
	if err := ok.Validate(); err != nil {
		t.Errorf("верный стек отклонён: %v", err)
	}
}
