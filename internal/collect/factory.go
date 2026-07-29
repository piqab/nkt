package collect

import (
	"fmt"
	"os"
	"runtime"
	"time"
)

// New builds the collector for the requested mode.
func New(mode, fixturesRoot, dockerSocket string, commandTimeout time.Duration) (Collector, error) {
	switch mode {
	case "fixtures":
		st, err := os.Stat(fixturesRoot)
		if err != nil {
			return nil, fmt.Errorf("снапшот %s недоступен: %w", fixturesRoot, err)
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
		return NewLocal(dockerSocket, commandTimeout), nil
	default:
		return nil, fmt.Errorf("неизвестный режим сбора данных: %q", mode)
	}
}
