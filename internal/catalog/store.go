package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Copy на диске хаба: <dir>/index.json, index.json.sig, source.json и
// файлы по путям «<key>/<путь>». Копия заменяется целиком: новая пишется
// рядом и переименовывается, так что наполовину записанной она не бывает.

type sourceInfo struct {
	Source    string `json:"source"`
	FetchedAt string `json:"fetched_at"`
}

// Open проверяет подпись и все файлы и собирает снимок (files — по
// «<key>/<путь>»).
func Open(index, sig []byte, files map[string][]byte, keys []ed25519.PublicKey, source string) (*Snapshot, error) {
	return open(index, sig, files, keys, sourceInfo{Source: source, FetchedAt: time.Now().UTC().Format(time.RFC3339)})
}

func open(index, sig []byte, files map[string][]byte, keys []ed25519.PublicKey, src sourceInfo) (*Snapshot, error) {
	if err := Verify(index, sig, keys); err != nil {
		return nil, err
	}
	var ix Index
	if err := json.Unmarshal(index, &ix); err != nil {
		return nil, fmt.Errorf("index.json: %w", err)
	}
	if err := ix.Validate(); err != nil {
		return nil, err
	}
	snap := &Snapshot{Index: ix, Source: src.Source, FetchedAt: src.FetchedAt, files: map[string][]byte{}}
	check := func(key string, refs map[string]FileRef) error {
		for rel, f := range refs {
			b, ok := files[key+"/"+rel]
			if !ok {
				return fmt.Errorf("%s/%s: missing", key, rel)
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != f.SHA256 {
				return fmt.Errorf("%s/%s: SHA-256 mismatch", key, rel)
			}
			snap.files[key+"/"+rel] = b
		}
		return nil
	}
	for _, p := range ix.Plugins {
		if err := check(p.Key, p.Files); err != nil {
			return nil, err
		}
	}
	for _, e := range ix.Examples {
		if err := check(e.Key, e.Files); err != nil {
			return nil, err
		}
	}
	return snap, nil
}

// refs — все файлы каталога: «<key>/<путь>» → отпечаток.
func (ix *Index) refs() map[string]FileRef {
	out := map[string]FileRef{}
	for _, p := range ix.Plugins {
		for rel, f := range p.Files {
			out[p.Key+"/"+rel] = f
		}
	}
	for _, e := range ix.Examples {
		for rel, f := range e.Files {
			out[e.Key+"/"+rel] = f
		}
	}
	return out
}

// Load — копия каталога с диска (проверяется так же, как скачанная: файл
// на диске мог быть изменён).
func Load(dir string, keys []ed25519.PublicKey) (*Snapshot, error) {
	index, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		return nil, err
	}
	sig, err := os.ReadFile(filepath.Join(dir, "index.json.sig"))
	if err != nil {
		return nil, err
	}
	var src sourceInfo
	if raw, err := os.ReadFile(filepath.Join(dir, "source.json")); err == nil {
		_ = json.Unmarshal(raw, &src)
	}
	var ix Index
	if err := json.Unmarshal(index, &ix); err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for p := range ix.refs() {
		if !safeKeyPath(p) {
			return nil, fmt.Errorf("bad path %q", p)
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		files[p] = b
	}
	return open(index, sig, files, keys, src)
}

func safeKeyPath(p string) bool {
	key, rel, ok := strings.Cut(p, "/")
	return ok && keyRe.MatchString(key) && SafePath(rel)
}

// save пишет проверенный каталог в dir (целиком, через переименование).
func save(dir string, index, sig []byte, snap *Snapshot) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".catalog-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	src, _ := json.Marshal(sourceInfo{Source: snap.Source, FetchedAt: snap.FetchedAt})
	for name, b := range map[string][]byte{"index.json": index, "index.json.sig": sig, "source.json": src} {
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o640); err != nil {
			return err
		}
	}
	for p, b := range snap.files {
		dst := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o640); err != nil {
			return err
		}
	}
	old := dir + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.Rename(old, dir)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

// Fetch скачивает каталог с base (адрес каталога, «…/catalog/»),
// проверяет и сохраняет в dir. Прежняя копия остаётся, если что-то не так.
func Fetch(ctx context.Context, client *http.Client, base string, keys []ed25519.PublicKey, dir string) (*Snapshot, error) {
	u, err := url.Parse(strings.TrimSuffix(base, "/") + "/")
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return nil, fmt.Errorf("bad catalog address %q", base)
	}
	get := func(rel string, limit int64) ([]byte, error) {
		ref, _ := url.Parse(rel)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.ResolveReference(ref).String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: HTTP %d", rel, resp.StatusCode)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit {
			return nil, fmt.Errorf("%s: larger than %d bytes", rel, limit)
		}
		return b, nil
	}
	index, err := get("index.json", MaxIndex)
	if err != nil {
		return nil, err
	}
	sig, err := get("index.json.sig", 1024)
	if err != nil {
		return nil, err
	}
	// Подпись — до разбора и до скачивания файлов по списку из index.json.
	if err := Verify(index, sig, keys); err != nil {
		return nil, err
	}
	var ix Index
	if err := json.Unmarshal(index, &ix); err != nil {
		return nil, err
	}
	if err := ix.Validate(); err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	var total int64
	for p, f := range ix.refs() {
		if total += f.Size; total > MaxTotal {
			return nil, errors.New("catalog is too large")
		}
		b, err := get(p, MaxFile)
		if err != nil {
			return nil, err
		}
		files[p] = b
	}
	snap, err := open(index, sig, files, keys, sourceInfo{Source: u.String(), FetchedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	if err := save(dir, index, sig, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// Import — каталог из архива tar.gz (index.json, index.json.sig и файлы,
// как их выкладывает CI): хаб без интернета.
func Import(r io.Reader, keys []ed25519.PublicKey, dir string) (*Snapshot, error) {
	gz, err := gzip.NewReader(io.LimitReader(r, MaxTotal+MaxIndex))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	got := map[string][]byte{}
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "catalog/")
		if name != "index.json" && name != "index.json.sig" && !safeKeyPath(name) {
			continue
		}
		if total += h.Size; total > MaxTotal || h.Size > MaxFile && name != "index.json" {
			return nil, errors.New("archive is too large")
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, io.LimitReader(tr, MaxIndex+MaxFile)); err != nil {
			return nil, err
		}
		got[name] = buf.Bytes()
	}
	index, sig := got["index.json"], got["index.json.sig"]
	if index == nil || sig == nil {
		return nil, errors.New("archive has no index.json and index.json.sig")
	}
	delete(got, "index.json")
	delete(got, "index.json.sig")
	snap, err := open(index, sig, got, keys, sourceInfo{Source: "upload", FetchedAt: time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	if err := save(dir, index, sig, snap); err != nil {
		return nil, err
	}
	return snap, nil
}
