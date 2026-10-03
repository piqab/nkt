package fail2ban

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/collect"
)

// Поддельный fail2ban-client: -t падает, если в проверяемом каталоге
// (-c, иначе root) есть джейл со словом BROKEN.
func fakeClient(t *testing.T, root string) {
	t.Helper()
	bin := t.TempDir()
	script := `#!/bin/sh
dir=` + root + `
[ "$1" = "-c" ] && dir=$2
if grep -rq BROKEN "$dir"; then
  f=$(grep -rl BROKEN "$dir" | head -1)
  echo "ERROR   Errors in jail from $f"
  exit 255
fi
echo OK
`
	if err := os.WriteFile(filepath.Join(bin, "fail2ban-client"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func TestConfigTestFindsBrokenAndPreexisting(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "jail.d"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "jail.conf"), []byte("[DEFAULT]\n"), 0o644)
	fakeClient(t, root)
	c := collect.NewLocal("", "", 10*time.Second)
	ctx := context.Background()

	if r := TestConfig(ctx, c, root, map[string]string{"jail.d/nkt-a.local": "[a]\nenabled = true\n"}); !r.OK {
		t.Fatalf("good template rejected: %+v", r)
	}
	r := TestConfig(ctx, c, root, map[string]string{"jail.d/nkt-a.local": "[a]\nBROKEN\n"})
	if r.OK || r.Preexisting || !strings.Contains(r.Output, root) || strings.Contains(r.Output, "nkt-f2b-test-") {
		t.Fatalf("broken template: %+v", r)
	}
	// Сломано у хоста ещё до шаблона.
	_ = os.WriteFile(filepath.Join(root, "jail.d", "openvpn.conf"), []byte("[openvpn]\nBROKEN\n"), 0o644)
	r = TestConfig(ctx, c, root, map[string]string{"jail.d/nkt-a.local": "[a]\nenabled = true\n"})
	if r.OK || !r.Preexisting {
		t.Fatalf("preexisting: %+v", r)
	}
	if ok, out := CheckCurrent(ctx, c); ok || !strings.Contains(out, "openvpn") {
		t.Fatalf("current: %v %q", ok, out)
	}
	// Сам каталог настроек не тронут.
	if _, err := os.Stat(filepath.Join(root, "jail.d", "nkt-a.local")); !os.IsNotExist(err) {
		t.Fatal("test wrote into the real config")
	}
}
