// nkt-catalog — сборка каталога плагинов и примеров для GitHub Pages (CI,
// .github/workflows/pages.yml):
//
//	nkt-catalog build -src examples -out DIR [-commit SHA] [-tested] [-failed a,b] [-prev index.json]
//	nkt-catalog sign -dir DIR          (закрытый ключ PEM — в переменной NKT_CATALOG_KEY)
//	nkt-catalog verify -dir DIR [-pub key.pem]
//	nkt-catalog pack -dir DIR -out catalog.tar.gz
//
// Ключ — ed25519 из openssl (genpkey -algorithm ed25519); подпись проверяет
// и «openssl pkeyutl -verify -rawin».
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/piqab/nkt/internal/catalog"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = build(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "pack":
		err = pack(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nkt-catalog:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: nkt-catalog build|sign|verify|pack …")
	os.Exit(2)
}

func build(args []string) error {
	fl := flag.NewFlagSet("build", flag.ExitOnError)
	src := fl.String("src", "examples", "examples directory")
	out := fl.String("out", "catalog", "output directory")
	commit := fl.String("commit", "", "repository commit")
	tested := fl.Bool("tested", false, "plugin tests were run (marks plugins as passed)")
	failed := fl.String("failed", "", "comma-separated plugins whose tests failed (left out)")
	prev := fl.String("prev", "", "published index.json (version bump check); missing file — skipped")
	_ = fl.Parse(args)
	opt := catalog.BuildOptions{Commit: *commit, Tested: *tested, Failed: map[string]bool{}}
	for _, k := range strings.Split(*failed, ",") {
		if k = strings.TrimSpace(k); k != "" {
			opt.Failed[k] = true
		}
	}
	if *prev != "" {
		if raw, err := os.ReadFile(*prev); err == nil {
			var ix catalog.Index
			if json.Unmarshal(raw, &ix) == nil {
				opt.Prev = &ix
			}
		}
	}
	ix, files, err := catalog.Build(*src, opt)
	if err != nil {
		return err
	}
	if err := catalog.WriteDir(*out, ix, files, nil); err != nil {
		return err
	}
	fmt.Printf("catalog: %d plugins, %d examples, %d files → %s\n", len(ix.Plugins), len(ix.Examples), len(files), *out)
	for k := range opt.Failed {
		fmt.Printf("left out (tests failed): %s\n", k)
	}
	return nil
}

func sign(args []string) error {
	fl := flag.NewFlagSet("sign", flag.ExitOnError)
	dir := fl.String("dir", "catalog", "catalog directory")
	_ = fl.Parse(args)
	// Ключ — только из окружения (секрет CI), не из аргументов: argv видят
	// все процессы машины.
	pemKey := os.Getenv("NKT_CATALOG_KEY")
	if pemKey == "" {
		return fmt.Errorf("NKT_CATALOG_KEY is empty")
	}
	key, err := catalog.ParsePrivateKey([]byte(pemKey))
	if err != nil {
		return err
	}
	index, err := os.ReadFile(filepath.Join(*dir, "index.json"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*dir, "index.json.sig"), catalog.Sign(key, index), 0o644)
}

func verify(args []string) error {
	fl := flag.NewFlagSet("verify", flag.ExitOnError)
	dir := fl.String("dir", "catalog", "catalog directory")
	pub := fl.String("pub", "", "public key PEM (default: the keys built into nkt)")
	_ = fl.Parse(args)
	keys := catalog.DefaultKeys()
	if *pub != "" {
		raw, err := os.ReadFile(*pub)
		if err != nil {
			return err
		}
		if keys, err = catalog.ParsePublicKeys(string(raw)); err != nil {
			return err
		}
	}
	snap, err := catalog.Load(*dir, keys)
	if err != nil {
		return err
	}
	fmt.Printf("ok: %d plugins, %d examples, commit %s\n", len(snap.Index.Plugins), len(snap.Index.Examples), snap.Index.Commit)
	return nil
}

// pack — архив для хаба без интернета («Каталог» → «Загрузить архив»).
func pack(args []string) error {
	fl := flag.NewFlagSet("pack", flag.ExitOnError)
	dir := fl.String("dir", "catalog", "catalog directory")
	out := fl.String("out", "catalog.tar.gz", "archive")
	_ = fl.Parse(args)
	var names []string
	err := filepath.WalkDir(*dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			rel, _ := filepath.Rel(*dir, p)
			if rel != "source.json" {
				names = append(names, filepath.ToSlash(rel))
			}
		}
		return err
	})
	if err != nil {
		return err
	}
	sort.Strings(names)
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(*dir, filepath.FromSlash(n)))
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: "catalog/" + n, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(b); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
