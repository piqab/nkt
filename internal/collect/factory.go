package collect

import (
	"github.com/piqab/nkt/internal/msgs"
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
			// Подсказка — тоже ошибка каталога: переводится вместе с
			// внешним текстом.
			hint := msgs.Errorf("collect.snapshotHintRepo")
			if runtime.GOOS == "linux" {
				hint = msgs.Errorf("collect.snapshotHintLinux")
			}
			return nil, msgs.Errorf("collect.snapshotDirectoryFound", fixturesRoot, hint)
		}
		if !st.IsDir() {
			return nil, msgs.Errorf("collect.snapshotDirectory", fixturesRoot)
		}
		return NewFixtures(fixturesRoot), nil
	case "local":
		// Local mode reads /etc/nginx, runs systemctl and iptables-save, and
		// talks to a unix socket. None of that exists anywhere but Linux, so
		// refusing here gives a clear message instead of a pile of empty
		// sources and a dashboard that looks broken.
		if runtime.GOOS != "linux" {
			return nil, msgs.Errorf("collect.localModeWorksOnlyLinux", runtime.GOOS)
		}
		return NewLocal(dockerSocket, podmanSocket, commandTimeout), nil
	default:
		return nil, msgs.Errorf("collect.unknownDataCollectionMode", mode)
	}
}
