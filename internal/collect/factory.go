package collect

import (
	"fmt"
	"os"
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
		return NewLocal(dockerSocket, commandTimeout), nil
	default:
		return nil, fmt.Errorf("неизвестный режим сбора данных: %q", mode)
	}
}
