package control

import "testing"

// Список образов: по одному на алиас и тип, только архитектура хоста,
// ссылка для lxc launch — с именем источника.
func TestParseLXDImages(t *testing.T) {
	out := `[
 {"aliases":[{"name":"debian/12"},{"name":"debian/bookworm"}],"architecture":"x86_64","type":"container","size":100,"fingerprint":"aaa","properties":{"description":"Debian bookworm amd64","os":"Debian","release":"bookworm"}},
 {"aliases":[{"name":"debian/12"}],"architecture":"x86_64","type":"virtual-machine","size":300,"fingerprint":"bbb","properties":{"os":"Debian"}},
 {"aliases":[{"name":"debian/12"}],"architecture":"aarch64","type":"container","size":100,"fingerprint":"ccc","properties":{"os":"Debian"}},
 {"aliases":[],"architecture":"x86_64","type":"container","size":1,"fingerprint":"ddd","properties":{}}
]`
	got, err := parseLXDImages(out, "images", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Ref != "images:debian/12" || got[0].Type != "container" || got[1].Type != "virtual-machine" {
		t.Fatalf("got %+v", got)
	}
	local, err := parseLXDImages(out, "local", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	var byFingerprint bool
	for _, im := range local {
		if im.Ref == "ddd" {
			byFingerprint = true
		}
	}
	if !byFingerprint {
		t.Errorf("локальный образ без алиаса должен идти по отпечатку: %+v", local)
	}
}
