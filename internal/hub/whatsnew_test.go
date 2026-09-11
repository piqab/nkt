package hub

import (
	"os"
	"strings"
	"testing"
)

// Заметки к версии — то, что хаб показывает в «О системе», когда
// появляется новая. Собираются они из WHATSNEW.md при выпуске тега, и
// пропущенный раздел раньше означал не пустоту, а подмену: релиз
// собирался из автосводки GitHub, и пользователь видел ссылку «Full
// Changelog» вместо текста изменений.
//
// Проверка здесь, а не в скрипте выпуска, потому что к моменту выпуска
// уже поздно: раздел пишется тем же коммитом, что и бамп VERSION, и
// забывается ровно тогда.
func TestWhatsNewCoversCurrentVersion(t *testing.T) {
	version, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Skipf("VERSION не прочитан (запуск вне репозитория): %v", err)
	}
	notes, err := os.ReadFile("../../WHATSNEW.md")
	if err != nil {
		t.Skipf("WHATSNEW.md не прочитан: %v", err)
	}

	current := strings.TrimSpace(string(version))
	if current == "" || current == "dev" {
		t.Skip("версия не задана")
	}
	// Заголовок пишется так же, как называется тег: «## v1.2.3 — дата».
	want := "## v" + current
	for _, line := range strings.Split(string(notes), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), want) {
			return
		}
	}
	t.Errorf("в WHATSNEW.md нет раздела «%s» для текущей версии.\n"+
		"Заметки к версии показываются в «О системе», и без раздела выпуск тега "+
		"откажет — добавьте его тем же коммитом, что и бамп VERSION.", want)
}

// Заметки из WHATSNEW.md должны доходить до «О системе» целиком.
//
// Проверяется тот же путь, которым они идут на самом деле: текст
// раздела → тело релиза → cleanReleaseNotes → поле notes. Раньше в этом
// месте оказывалась автосводка GitHub — одна строка со ссылкой «Full
// Changelog», — и «полный текст изменений» превращался в ссылку.
func TestReleaseNotesSurviveWhole(t *testing.T) {
	raw, err := os.ReadFile("../../WHATSNEW.md")
	if err != nil {
		t.Skipf("WHATSNEW.md не прочитан: %v", err)
	}
	// Берём первый раздел файла — так же, как это делает
	// scripts/release-notes.sh для свежего тега.
	var section []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## v") {
			if len(section) > 0 {
				break
			}
			section = append(section, line)
			continue
		}
		if len(section) > 0 {
			section = append(section, line)
		}
	}
	body := strings.TrimSpace(strings.Join(section, "\n"))
	if body == "" {
		t.Fatal("в WHATSNEW.md нет ни одного раздела")
	}

	notes := cleanReleaseNotes(body)
	if notes != body {
		t.Errorf("текст изменился по дороге: было %d символов, стало %d", len(body), len(notes))
	}
	if strings.Count(notes, "\n") < 2 {
		t.Errorf("до интерфейса дошла одна строка — так выглядела автосводка со ссылкой:\n%s", notes)
	}
	if strings.Contains(notes, "Full Changelog") {
		t.Errorf("вместо текста изменений — автосводка GitHub:\n%s", notes)
	}
}
