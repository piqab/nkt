package catalog

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Свои плагины (1.17.1): администратор загружает архив папки плагина в
// хаб — без подписи каталога, но с той же статической проверкой (lint);
// на хабе плагин не запускается. Хранит их хаб (настройка
// plugins.local — едет в экспорт); здесь — разбор архива, проверка и
// реестр в памяти, из которого PluginPackages отдаёт их рядом с
// плагинами каталога под ключом «local:<имя>».

// LocalPrefix — ключ своего плагина: «local:<имя>» (не пересечётся с
// плагином каталога, даже если там появится такое же имя).
const LocalPrefix = "local:"

// Пределы архива своего плагина.
const (
	UploadMaxFile  = 2 << 20
	UploadMaxTotal = 10 << 20
	UploadMaxFiles = 64
	// LocalHistory — сколько версий кода хранить.
	LocalHistory = 10
)

// Что берётся из архива; остальное (тесты, кэш Python, свои файлы) —
// пропускается и называется в ответе.
var localFileRe = regexp.MustCompile(`^(deploy/[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}\.(py|json)|README(\.en)?\.md|screens/(ru|en)/[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}\.png)$`)

// ReadUpload — файлы архива папки плагина (.tar.gz или .zip): общая
// верхняя папка снимается; ссылки, устройства и выход за папку — ошибка.
func ReadUpload(data []byte) (files map[string][]byte, skipped []string, err error) {
	if len(data) > UploadMaxTotal+UploadMaxTotal/2 {
		return nil, nil, fmt.Errorf("archive larger than %d MB", UploadMaxTotal>>20)
	}
	raw := map[string][]byte{}
	var total int64
	add := func(name string, size int64, r io.Reader) error {
		// «..» и абсолютные пути — отказ, а не молчаливая чистка: такой
		// архив сделан, чтобы выйти за папку.
		orig := strings.ReplaceAll(name, "\\", "/")
		for _, part := range strings.Split(orig, "/") {
			if part == ".." {
				return fmt.Errorf("bad path %q", name)
			}
		}
		if strings.HasPrefix(orig, "/") {
			return fmt.Errorf("absolute path %q", name)
		}
		name = strings.TrimPrefix(path.Clean("/"+orig), "/")
		if name == "" {
			return fmt.Errorf("bad path %q", orig)
		}
		if len(raw) >= UploadMaxFiles {
			return fmt.Errorf("more than %d files", UploadMaxFiles)
		}
		if size > UploadMaxFile {
			return fmt.Errorf("%s: larger than %d MB", name, UploadMaxFile>>20)
		}
		if total += size; total > UploadMaxTotal {
			return fmt.Errorf("files larger than %d MB in total", UploadMaxTotal>>20)
		}
		b, err := io.ReadAll(io.LimitReader(r, UploadMaxFile+1))
		if err != nil {
			return err
		}
		if int64(len(b)) > UploadMaxFile {
			return fmt.Errorf("%s: larger than %d MB", name, UploadMaxFile>>20)
		}
		raw[name] = b
		return nil
	}
	switch {
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, nil, err
		}
		tr := tar.NewReader(io.LimitReader(gz, UploadMaxTotal*2))
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, nil, err
			}
			switch h.Typeflag {
			case tar.TypeDir, tar.TypeXGlobalHeader, tar.TypeXHeader:
				continue
			case tar.TypeReg:
				if err := add(h.Name, h.Size, tr); err != nil {
					return nil, nil, err
				}
			default:
				return nil, nil, fmt.Errorf("%s: links and special files are not allowed", h.Name)
			}
		}
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, nil, err
		}
		for _, f := range zr.File {
			mode := f.Mode()
			if mode.IsDir() {
				continue
			}
			if !mode.IsRegular() {
				return nil, nil, fmt.Errorf("%s: links and special files are not allowed", f.Name)
			}
			if f.UncompressedSize64 > UploadMaxFile {
				return nil, nil, fmt.Errorf("%s: larger than %d MB", f.Name, UploadMaxFile>>20)
			}
			rc, err := f.Open()
			if err != nil {
				return nil, nil, err
			}
			err = add(f.Name, int64(f.UncompressedSize64), rc)
			rc.Close()
			if err != nil {
				return nil, nil, err
			}
		}
	default:
		return nil, nil, errors.New("not a .tar.gz or .zip archive")
	}
	// Общая верхняя папка (url-check/deploy/… → deploy/…).
	prefix := ""
	for name := range raw {
		top, _, ok := strings.Cut(name, "/")
		if !ok || (prefix != "" && prefix != top) || top == "deploy" || top == "screens" {
			prefix = ""
			break
		}
		prefix = top
	}
	files = map[string][]byte{}
	for name, b := range raw {
		rel := name
		if prefix != "" {
			rel = strings.TrimPrefix(name, prefix+"/")
		}
		if !localFileRe.MatchString(rel) || !SafePath(rel) {
			skipped = append(skipped, rel)
			continue
		}
		files[rel] = b
	}
	sort.Strings(skipped)
	return files, skipped, nil
}

// LocalUpload — проверенный загруженный плагин (ещё не сохранён).
type LocalUpload struct {
	Plugin   Plugin            `json:"plugin"`
	Code     string            `json:"code"`
	Manifest string            `json:"manifest"`
	SHA256   string            `json:"sha256"`
	Problems []string          `json:"problems"`
	Skipped  []string          `json:"skipped,omitempty"`
	Files    map[string][]byte `json:"-"`
}

// CheckUpload — пакет из файлов архива и статическая проверка lint.
func CheckUpload(files map[string][]byte, skipped []string) (*LocalUpload, error) {
	raw, ok := files["deploy/nkt-plugin.json"]
	if !ok {
		return nil, errors.New("no deploy/nkt-plugin.json in the archive")
	}
	var p Plugin
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("deploy/nkt-plugin.json: %w", err)
	}
	dir, err := os.MkdirTemp("", "nkt-upload-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for rel, b := range files {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, b, 0o600); err != nil {
			return nil, err
		}
	}
	up := &LocalUpload{Problems: LintWith(dir, LintOptions{Upload: true}), Skipped: skipped, Files: files, Manifest: string(raw)}
	if up.Problems == nil {
		up.Problems = []string{}
	}
	code := files["deploy/"+p.File]
	up.Code = string(code)
	sum := sha256.Sum256(code)
	up.SHA256 = hex.EncodeToString(sum[:])
	p.Key = LocalPrefix + p.Name
	p.Files = map[string]FileRef{}
	p.Readme = map[string]string{}
	p.Screens = map[string][]string{}
	for rel, b := range files {
		if rel == "deploy/test_plugin.py" {
			continue
		}
		s := sha256.Sum256(b)
		p.Files[rel] = FileRef{SHA256: hex.EncodeToString(s[:]), Size: int64(len(b))}
		switch {
		case rel == "README.md":
			p.Readme["ru"] = rel
		case rel == "README.en.md":
			p.Readme["en"] = rel
		case strings.HasPrefix(rel, "screens/"):
			lang := strings.Split(rel, "/")[1]
			p.Screens[lang] = append(p.Screens[lang], rel)
		}
	}
	for _, l := range p.Screens {
		sort.Strings(l)
	}
	if p.Protocol == 0 {
		p.Protocol = 1
	}
	up.Plugin = p
	return up, nil
}

// LocalVersion — версия своего плагина в истории (код и пакет).
type LocalVersion struct {
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	At       string `json:"at"`
	User     string `json:"user,omitempty"`
	Code     string `json:"code"`
	Manifest string `json:"manifest"`
}

// LocalPlugin — свой плагин: файлы текущей версии и история кода.
type LocalPlugin struct {
	Plugin   Plugin            `json:"plugin"`
	Files    map[string][]byte `json:"files"`
	Versions []LocalVersion    `json:"versions"`
}

var (
	localMu sync.RWMutex
	local   = map[string]LocalPlugin{}
)

// SetLocal — свои плагины хаба (после чтения настройки и после правки).
func SetLocal(m map[string]LocalPlugin) {
	localMu.Lock()
	defer localMu.Unlock()
	local = m
}

// Local — свои плагины по имени.
func Local() map[string]LocalPlugin {
	localMu.RLock()
	defer localMu.RUnlock()
	out := make(map[string]LocalPlugin, len(local))
	for k, v := range local {
		out[k] = v
	}
	return out
}

// LocalFile — файл своего плагина (README, скриншот) по ключу «local:имя».
func LocalFile(key, rel string) ([]byte, bool) {
	name, ok := strings.CutPrefix(key, LocalPrefix)
	if !ok {
		return nil, false
	}
	localMu.RLock()
	defer localMu.RUnlock()
	lp, ok := local[name]
	if !ok {
		return nil, false
	}
	if _, listed := lp.Plugin.Files[rel]; !listed {
		return nil, false
	}
	b, ok := lp.Files[rel]
	return b, ok
}

func localPackages() []PluginPackage {
	localMu.RLock()
	defer localMu.RUnlock()
	out := make([]PluginPackage, 0, len(local))
	for _, lp := range local {
		if code, ok := lp.Files["deploy/"+lp.Plugin.File]; ok {
			out = append(out, PluginPackage{Plugin: lp.Plugin, Content: code})
		}
	}
	return out
}
