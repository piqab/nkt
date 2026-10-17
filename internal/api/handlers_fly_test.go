package api

import (
	"strings"
	"testing"
)

// wg syncconf приводит интерфейс точно к файлу, а закрытого ключа в файле
// нет — сразу за ним ключ ставится заново, иначе туннель молча глохнет.
func TestFlyHostRefreshRestoresKey(t *testing.T) {
	i := strings.Index(flyHostRefresh, "wg syncconf")
	j := strings.Index(flyHostRefresh, `wg set "$1" private-key "$7"`)
	if i < 0 || j < i {
		t.Fatalf("private key is not re-set after syncconf:\n%s", flyHostRefresh)
	}
}
