package hub

import "testing"

// Цель символической ссылки из архива не может вести наружу из каталога
// распаковки — ни абсолютная, ни через «..».
func TestSafeSymlinkTarget(t *testing.T) {
	root := "/tmp/tc.tmp"
	for linkname, ok := range map[string]bool{
		"bin/go":            true,
		"../go/bin/go":      true, // /tmp/tc.tmp/x/../go/bin/go остаётся внутри
		"/etc/passwd":       false,
		"../../etc/passwd":  false,
		"../../tc.tmp2/bin": false,
	} {
		_, err := safeSymlinkTarget(root, root+"/x/link", linkname)
		if (err == nil) != ok {
			t.Errorf("%q: err=%v, ждали ok=%v", linkname, err, ok)
		}
	}
}
