// Package catalogtest — каталог из examples/ репозитория для тестов:
// собирается, подписывается временным ключом и проверяется тем же путём,
// что настоящий (catalog.Open).
package catalogtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"runtime"

	"github.com/piqab/nkt/internal/catalog"
)

// ExamplesDir — examples/ репозитория.
func ExamplesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "examples")
}

// Signed — каталог из src, подписанный новым ключом: index.json, подпись,
// файлы и открытый ключ.
func Signed(src string) (index, sig []byte, files map[string][]byte, pub ed25519.PublicKey, err error) {
	ix, files, err := catalog.Build(src, catalog.BuildOptions{Commit: "test", Tested: true})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	index, _ = json.Marshal(ix)
	return index, catalog.Sign(priv, index), files, pub, nil
}

// Use делает каталог из examples/ текущим (catalog.SetCurrent).
func Use() error {
	index, sig, files, pub, err := Signed(ExamplesDir())
	if err != nil {
		return err
	}
	snap, err := catalog.Open(index, sig, files, []ed25519.PublicKey{pub}, "test")
	if err != nil {
		return err
	}
	catalog.SetCurrent(snap)
	return nil
}
