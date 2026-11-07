package control

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/parse"
)

// Каталог, где лежит compose-стек, — настройка хоста (NKT_COMPOSE_ROOTS).
// До неё редактор признавал compose-файлы только под /home и под каталогом
// стеков из профилей, и стек, который оператор держит в /opt, ловил отказ
// «файл вне разрешённых каталогов» без ответа на вопрос, куда можно; теперь
// отказ называет каталоги, а список корней задаётся на хосте.
func TestComposeRootsConfigurable(t *testing.T) {
	m := configsSetup(t)

	if _, err := m.checkPath("/home/alice/docker-compose.yml"); err != nil {
		t.Errorf("/home без настройки должен оставаться разрешённым: %v", err)
	}
	stacks := parse.ComposeStacksDir + "/shop/docker-compose.yml"
	if _, err := m.checkPath(stacks); err != nil {
		t.Errorf("каталог стеков без настройки должен оставаться разрешённым: %v", err)
	}

	// Отказ: тот же sentinel (по нему API отвечает 403) и список каталогов,
	// где compose-файлам место.
	_, err := m.checkPath("/opt/sunshine/docker-compose.yml")
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("ожидали отказ песочницы, получили %v", err)
	}
	if !strings.Contains(err.Error(), parse.ComposeStacksDir) {
		t.Errorf("отказ обязан называть разрешённые каталоги, а он: %v", err)
	}

	m.cfg.ComposeRoots = []string{"/opt"}
	if _, err := m.checkPath("/opt/sunshine/docker-compose.yml"); err != nil {
		t.Errorf("NKT_COMPOSE_ROOTS=/opt: %v", err)
	}
	// /home из списка убран — но каталог стеков остаётся всегда: применение
	// профиля пишет именно туда, и настройка, отменяющая это, сломала бы
	// выкладку, а не редактор.
	if _, err := m.checkPath(stacks); err != nil {
		t.Errorf("каталог стеков исчез из-за настройки: %v", err)
	}
	if _, err := m.checkPath("/home/alice/docker-compose.yml"); err == nil {
		t.Errorf("/home вне настроенного списка должен отказываться")
	}
	// Выборщик каталогов и создание каталога видят те же корни, что и
	// проверка пути, — иначе отказ нажимает человеку после «Создать».
	if roots := m.CategoryRoots()[model.ServiceDocker]; !slices.Contains(roots, "/opt") {
		t.Errorf("CategoryRoots для docker не показывает настроенный корень: %v", roots)
	}
}
