package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// BuildOptions — сборка каталога (cmd/nkt-catalog build, CI).
type BuildOptions struct {
	// Commit — коммит репозитория, из которого собран каталог.
	Commit string
	// Failed — плагины, у которых упал тест на подделках: в каталог не
	// попадают. Tested — тесты прогоняли (иначе отметки «passed» нет).
	Failed map[string]bool
	Tested bool
	// Prev — опубликованный каталог: файлы плагина изменились, а версия
	// та же — ошибка сборки (хабы не узнали бы об обновлении).
	Prev *Index
	// Now — время сборки (тестам).
	Now time.Time
}

var pyVersionRe = regexp.MustCompile(`(?m)^VERSION = "([^"]+)"`)

// Build собирает каталог из каталога примеров src (examples/): index.json
// и файлы по ключу «<key>/<путь>».
func Build(src string, opt BuildOptions) (*Index, map[string][]byte, error) {
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	ix := &Index{Format: Format, Commit: opt.Commit, Generated: opt.Now.UTC().Format(time.RFC3339), Plugins: []Plugin{}, Examples: []Example{}}
	files := map[string][]byte{}
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		key := e.Name()
		if !e.IsDir() || !keyRe.MatchString(key) {
			continue
		}
		dir := filepath.Join(src, key)
		read := func(rel string) ([]byte, bool) {
			b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
			return b, err == nil
		}
		add := func(m map[string]FileRef, rel string) bool {
			b, ok := read(rel)
			if !ok {
				return false
			}
			sum := sha256.Sum256(b)
			m[rel] = FileRef{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))}
			files[key+"/"+rel] = b
			return true
		}
		readme := map[string]string{}
		title := map[string]string{}
		for lang, name := range map[string]string{"ru": "README.md", "en": "README.en.md"} {
			if b, ok := read(name); ok {
				readme[lang] = name
				first, _, _ := strings.Cut(string(b), "\n")
				title[lang] = strings.TrimSpace(strings.TrimPrefix(first, "#"))
			}
		}

		if raw, ok := read("deploy/nkt-plugin.json"); ok && !opt.Failed[key] {
			var p Plugin
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, nil, fmt.Errorf("%s: nkt-plugin.json: %w", key, err)
			}
			p.Key = key
			if p.Protocol == 0 {
				p.Protocol = 1
			}
			if !verRe.MatchString(p.Version) {
				return nil, nil, fmt.Errorf("%s: nkt-plugin.json needs version X.Y.Z", key)
			}
			p.Files = map[string]FileRef{}
			if !add(p.Files, "deploy/nkt-plugin.json") || !add(p.Files, "deploy/"+p.File) {
				return nil, nil, fmt.Errorf("%s: no plugin file %q", key, p.File)
			}
			// Версия в файле плагина (его манифест) — та же, что в каталоге.
			if m := pyVersionRe.FindSubmatch(files[key+"/deploy/"+p.File]); m != nil && string(m[1]) != p.Version {
				return nil, nil, fmt.Errorf("%s: VERSION %s in %s, %s in nkt-plugin.json", key, m[1], p.File, p.Version)
			}
			p.Readme = map[string]string{}
			for lang, name := range readme {
				add(p.Files, name)
				p.Readme[lang] = name
			}
			p.Screens = map[string][]string{}
			for _, lang := range []string{"ru", "en"} {
				shots, _ := filepath.Glob(filepath.Join(dir, "screens", lang, "*.png"))
				sort.Strings(shots)
				for _, s := range shots {
					rel := path.Join("screens", lang, filepath.Base(s))
					add(p.Files, rel)
					p.Screens[lang] = append(p.Screens[lang], rel)
				}
			}
			if opt.Tested {
				p.Tests = "passed"
			}
			if err := sameVersionSameFiles(opt.Prev, p); err != nil {
				return nil, nil, err
			}
			ix.Plugins = append(ix.Plugins, p)
		}

		pipes, _ := fs.Glob(os.DirFS(dir), "pipeline*.yaml")
		ui, hasUI := read("nkt-example.json")
		if len(pipes) == 0 && !hasUI {
			continue
		}
		ex := Example{Key: key, Title: title, Files: map[string]FileRef{}, Readme: map[string]string{}}
		for lang, name := range readme {
			add(ex.Files, name)
			ex.Readme[lang] = name
		}
		sort.Strings(pipes)
		for _, f := range pipes {
			add(ex.Files, f)
			ex.Pipelines = append(ex.Pipelines, f)
		}
		if hasUI {
			if !json.Valid(ui) {
				return nil, nil, fmt.Errorf("%s: nkt-example.json is not JSON", key)
			}
			add(ex.Files, "nkt-example.json")
			ex.UI = json.RawMessage(ui)
		}
		ix.Examples = append(ix.Examples, ex)
	}
	if err := ix.Validate(); err != nil {
		return nil, nil, err
	}
	return ix, files, nil
}

func sameVersionSameFiles(prev *Index, p Plugin) error {
	if prev == nil {
		return nil
	}
	for _, old := range prev.Plugins {
		if old.Key != p.Key || old.Version != p.Version {
			continue
		}
		for rel, f := range p.Files {
			if strings.HasPrefix(rel, "screens/") || strings.HasPrefix(rel, "README") {
				continue // справка и скриншоты — без смены версии
			}
			if o, ok := old.Files[rel]; !ok || o.SHA256 != f.SHA256 {
				return fmt.Errorf("%s: %s changed but version stays %s — raise version in nkt-plugin.json (and VERSION in the plugin)", p.Key, rel, p.Version)
			}
		}
	}
	return nil
}

// WriteDir пишет каталог в out: index.json, подпись (если sig не nil) и
// файлы по путям «<key>/<путь>».
func WriteDir(out string, ix *Index, files map[string][]byte, sig []byte) error {
	raw, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "index.json"), raw, 0o644); err != nil {
		return err
	}
	if sig != nil {
		if err := os.WriteFile(filepath.Join(out, "index.json.sig"), sig, 0o644); err != nil {
			return err
		}
	}
	for p, b := range files {
		dst := filepath.Join(out, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
