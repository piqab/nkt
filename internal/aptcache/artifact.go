package aptcache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Файлы по ссылке: GET /nkt/artifact?url=https://… — хост просит файл у
// хаба, хаб качает его своим интернетом, кладёт в кэш и отдаёт. Так на
// хост без выхода наружу попадают установщик k3s, бинарник k3s и его
// airgap-образы, Cilium CLI, ключ репозитория Kubernetes, образы машин —
// и каждый из них уходит в интернет один раз на хаб, а не по разу на
// узел. Ссылка внутри TLS не нужна: хост ходит на хаб по обычному HTTP
// через проброс, хаб к источнику — по HTTPS.
//
// Неизменяемое (версия в пути, .deb) лежит бессрочно; остальное
// (get.k3s.io, …/releases/latest/…, «текущий» облачный образ)
// перекачивается, когда старше artifactTTL. Если источник недоступен, а
// устаревшая копия есть — отдаётся она: хосту важнее получить файл.

const artifactTTL = 6 * time.Hour

// ArtifactImmutable — не меняется ли файл под этой ссылкой.
func ArtifactImmutable(u *url.URL) bool {
	if Cacheable(u.Path) {
		return true
	}
	if i := strings.Index(u.Path, "/releases/download/"); i >= 0 {
		seg, _, _ := strings.Cut(strings.TrimPrefix(u.Path[i:], "/releases/download/"), "/")
		return seg != "" && seg != "latest"
	}
	return false
}

func (c *Cache) serveArtifact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, err := url.Parse(r.URL.Query().Get("url"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		http.Error(w, "nkt cache: bad url", http.StatusBadRequest)
		return
	}
	p := path.Clean("/" + u.Path)
	if strings.HasSuffix(u.Path, "/") || p == "/" {
		p = path.Join(p, "index")
	}
	rel := path.Join("artifacts", u.Host, p)
	if strings.Contains(rel, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	file := filepath.Join(c.dir, filepath.FromSlash(rel))
	ttl := artifactTTL
	if ArtifactImmutable(u) {
		ttl = 0
	}
	if c.fresh(rel, ttl) {
		c.hits.Add(1)
		c.serveFile(w, r, file)
		return
	}
	c.misses.Add(1)
	if err := c.fetch(r.Context(), rel, file, u.String()); err != nil {
		if c.has(rel) {
			// Источник не ответил, но копия есть — лучше она, чем ничего.
			w.Header().Set("X-Cache-Stale", "1")
			c.serveFile(w, r, file)
			return
		}
		var se *statusError
		if errors.As(err, &se) {
			http.Error(w, se.Error(), se.code)
			return
		}
		http.Error(w, "nkt cache: "+err.Error(), http.StatusBadGateway)
		return
	}
	c.serveFile(w, r, file)
}

// Prefetch кладёт файл по ссылке в кэш заранее (подготовка кластера на
// хабе: хаб качает своим интернетом то, что узлы потом возьмут с него) и
// возвращает размер; свежая копия не перекачивается.
func (c *Cache) Prefetch(ctx context.Context, rawURL string) (int64, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return 0, errors.New("bad url")
	}
	p := path.Clean("/" + u.Path)
	if strings.HasSuffix(u.Path, "/") || p == "/" {
		p = path.Join(p, "index")
	}
	rel := path.Join("artifacts", u.Host, p)
	file := filepath.Join(c.dir, filepath.FromSlash(rel))
	ttl := artifactTTL
	if ArtifactImmutable(u) {
		ttl = 0
	}
	if !c.fresh(rel, ttl) {
		if err := c.fetch(ctx, rel, file, u.String()); err != nil {
			return 0, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[rel]; ok {
		return e.size, nil
	}
	return 0, nil
}

// PrefetchBytes — Prefetch плюс содержимое (не больше limit байт).
func (c *Cache) PrefetchBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if _, err := c.Prefetch(ctx, rawURL); err != nil {
		return nil, err
	}
	u, _ := url.Parse(rawURL)
	p := path.Clean("/" + u.Path)
	if strings.HasSuffix(u.Path, "/") || p == "/" {
		p = path.Join(p, "index")
	}
	f, err := os.Open(filepath.Join(c.dir, filepath.FromSlash(path.Join("artifacts", u.Host, p))))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}
