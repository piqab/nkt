package api

import (
	"context"

	"github.com/piqab/nkt/internal/collect"
)

// virsh переводит всё: заголовки таблиц, состояния доменов и сетей,
// сообщения об ошибках. Разбор по словам «active» или «running» на
// русской или немецкой машине молча перестаёт работать — список сетей
// оказывается пустым, домен «работает» считается остановленным.
//
// Поэтому всё, чей вывод разбирается, запускается через это окружение.
var cCollate = map[string]string{"LC_ALL": "C", "LANG": "C"}

// RunTooling выполняет команду вне песочницы с предсказуемым языком
// вывода.
func RunTooling(ctx context.Context, argv ...string) (collect.CommandResult, error) {
	return RunUnrestrictedEnv(ctx, cCollate, nil, argv...)
}
