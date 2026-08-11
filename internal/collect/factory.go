package collect

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

// New builds the collector for the requested mode.
func New(mode, fixturesRoot, dockerSocket, podmanSocket string, commandTimeout time.Duration) (Collector, error) {
	switch mode {
	case "fixtures":
		st, err := os.Stat(fixturesRoot)
		if err != nil {
			hint := "запускайте из корня репозитория или задайте NKT_FIXTURES_ROOT"
			if runtime.GOOS == "linux" {
				hint = "если это боевой хост, нужен режим NKT_MODE=local; " +
					"для снапшота — запуск из корня репозитория или NKT_FIXTURES_ROOT"
			}
			return nil, fmt.Errorf("каталог снапшота %s не найден. %s", fixturesRoot, hint)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("снапшот %s не является каталогом", fixturesRoot)
		}
		return NewFixtures(fixturesRoot), nil
	case "local":
		// Local mode reads /etc/nginx, runs systemctl and iptables-save, and
		// talks to a unix socket. None of that exists anywhere but Linux, so
		// refusing here gives a clear message instead of a pile of empty
		// sources and a dashboard that looks broken.
		if runtime.GOOS != "linux" {
			return nil, fmt.Errorf(
				"режим local работает только на Linux, а система — %s. "+
					"Для разработки используйте NKT_MODE=fixtures", runtime.GOOS)
		}
		return NewLocal(dockerSocket, podmanSocket, commandTimeout), nil
	default:
		return nil, fmt.Errorf("неизвестный режим сбора данных: %q", mode)
	}
}
