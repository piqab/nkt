// Package aptcache — кэширующий HTTP-прокси для пакетов: хосты качают
// через хаб, и каждый .deb уходит в интернет один раз. Кэшируется только
// неизменяемое — файлы из pool/ и by-hash/ (у них имя включает версию или
// хэш); индексы репозиториев (dists/…) проходят насквозь, чтобы хост
// всегда видел свежие. HTTPS через CONNECT просто туннелируется: внутрь
// TLS прокси не смотрит и кэшировать там нечего.
//
// Прокси не слушает сеть сам: хаб отдаёт его хостам через обратный проброс
// порта по SSH (см. internal/hub/aptproxy.go), так что чужому он
// недоступен.
package aptcache

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Cache — прокси с кэшем на диске.
type Cache struct {
	dir      string
	maxBytes atomic.Int64
	client   *http.Client

	mu       sync.Mutex
	entries  map[string]*entry // ключ — относительный путь в кэше
	total    int64
	inflight map[string]*download

	hits, misses, bytesServed, bytesFetched atomic.Int64
}

type entry struct {
	size     int64
	lastUsed time.Time
}

type download struct {
	done chan struct{}
	err  error
}

// New открывает кэш в dir, подхватывая уже лежащие файлы.
func New(dir string, maxBytes int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir, entries: map[string]*entry{}, inflight: map[string]*download{},
		client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: 8}}}
	c.maxBytes.Store(maxBytes)
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".part") {
			os.Remove(p)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		c.entries[filepath.ToSlash(rel)] = &entry{size: info.Size(), lastUsed: info.ModTime()}
		c.total += info.Size()
		return nil
	})
	return c, nil
}

// Stats — что показать в «О системе».
type Stats struct {
	Entries      int   `json:"entries"`
	SizeBytes    int64 `json:"size_bytes"`
	MaxBytes     int64 `json:"max_bytes"`
	Hits         int64 `json:"hits"`
	Misses       int64 `json:"misses"`
	BytesServed  int64 `json:"bytes_served"`
	BytesFetched int64 `json:"bytes_fetched"`
}

func (c *Cache) Stats() Stats {
	c.mu.Lock()
	n, total := len(c.entries), c.total
	c.mu.Unlock()
	return Stats{Entries: n, SizeBytes: total, MaxBytes: c.maxBytes.Load(), Hits: c.hits.Load(), Misses: c.misses.Load(),
		BytesServed: c.bytesServed.Load(), BytesFetched: c.bytesFetched.Load()}
}

// SetMaxBytes меняет лимит и сразу вытесняет лишнее.
func (c *Cache) SetMaxBytes(n int64) {
	c.maxBytes.Store(n)
	c.mu.Lock()
	c.evictLocked()
	c.mu.Unlock()
}

// Clear удаляет всё из кэша.
func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for rel := range c.entries {
		os.Remove(filepath.Join(c.dir, filepath.FromSlash(rel)))
	}
	c.entries = map[string]*entry{}
	c.total = 0
	return nil
}

// Cacheable — стоит ли класть путь в кэш: только то, что не меняется под
// тем же именем.
func Cacheable(p string) bool {
	if strings.Contains(p, "/by-hash/") {
		return true
	}
	if strings.Contains(p, "/dists/") {
		return false
	}
	if strings.Contains(p, "/pool/") {
		return true
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".deb", ".udeb", ".ddeb", ".rpm", ".apk":
		return true
	}
	return false
}

func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		c.tunnel(w, r)
		return
	}
	if !r.URL.IsAbs() {
		http.Error(w, "nkt apt cache: proxy requests only", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		http.Error(w, "unsupported scheme", http.StatusBadRequest)
		return
	}
	if !Cacheable(r.URL.Path) || r.Method == http.MethodHead {
		c.passThrough(w, r)
		return
	}
	rel := path.Join(r.URL.Host, path.Clean("/"+r.URL.Path))
	if strings.Contains(rel, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	file := filepath.Join(c.dir, filepath.FromSlash(rel))

	if c.touch(rel) {
		c.hits.Add(1)
		c.serveFile(w, r, file)
		return
	}
	c.misses.Add(1)
	if err := c.fetch(r.Context(), rel, file, r.URL.String()); err != nil {
		var se *statusError
		if errors.As(err, &se) {
			http.Error(w, se.Error(), se.code)
			return
		}
		http.Error(w, "nkt apt cache: "+err.Error(), http.StatusBadGateway)
		return
	}
	c.serveFile(w, r, file)
}

type statusError struct{ code int }

func (e *statusError) Error() string { return "upstream " + http.StatusText(e.code) }

// touch отмечает попадание; false — файла в кэше нет.
func (c *Cache) touch(rel string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[rel]
	if !ok {
		return false
	}
	e.lastUsed = time.Now()
	return true
}

// fetch скачивает файл в кэш; одновременные запросы одного файла ждут
// одну загрузку.
func (c *Cache) fetch(ctx context.Context, rel, file, url string) error {
	c.mu.Lock()
	if d, ok := c.inflight[rel]; ok {
		c.mu.Unlock()
		<-d.done
		return d.err
	}
	d := &download{done: make(chan struct{})}
	c.inflight[rel] = d
	c.mu.Unlock()

	d.err = c.download(ctx, rel, file, url)
	c.mu.Lock()
	delete(c.inflight, rel)
	c.mu.Unlock()
	close(d.done)
	return d.err
}

func (c *Cache) download(ctx context.Context, rel, file, url string) error {
	// Загрузка не привязана к запросу, который её начал: если он оборвётся,
	// файл всё равно доедет, и следующий получит его из кэша.
	dlCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "nkt-apt-cache")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode}
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if resp.ContentLength > 0 && n != resp.ContentLength {
		os.Remove(tmp)
		return errors.New("short download")
	}
	if err := os.Rename(tmp, file); err != nil {
		os.Remove(tmp)
		return err
	}
	c.bytesFetched.Add(n)
	c.mu.Lock()
	if old, ok := c.entries[rel]; ok {
		c.total -= old.size
	}
	c.entries[rel] = &entry{size: n, lastUsed: time.Now()}
	c.total += n
	c.evictLocked()
	c.mu.Unlock()
	_ = ctx
	return nil
}

// evictLocked убирает самое давно не спрошенное, пока не влезем в лимит.
func (c *Cache) evictLocked() {
	max := c.maxBytes.Load()
	if max <= 0 || c.total <= max {
		return
	}
	type kv struct {
		rel string
		e   *entry
	}
	all := make([]kv, 0, len(c.entries))
	for rel, e := range c.entries {
		all = append(all, kv{rel, e})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].e.lastUsed.Before(all[j].e.lastUsed) })
	for _, it := range all {
		if c.total <= max {
			break
		}
		if _, inflight := c.inflight[it.rel]; inflight {
			continue
		}
		os.Remove(filepath.Join(c.dir, filepath.FromSlash(it.rel)))
		c.total -= it.e.size
		delete(c.entries, it.rel)
	}
}

func (c *Cache) serveFile(w http.ResponseWriter, r *http.Request, file string) {
	f, err := os.Open(file)
	if err != nil {
		http.Error(w, "cache read: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Cache", "nkt")
	cw := &countWriter{ResponseWriter: w}
	http.ServeContent(cw, r, filepath.Base(file), st.ModTime(), f)
	c.bytesServed.Add(cw.n)
}

type countWriter struct {
	http.ResponseWriter
	n int64
}

func (cw *countWriter) Write(b []byte) (int, error) {
	n, err := cw.ResponseWriter.Write(b)
	cw.n += int64(n)
	return n, err
}

// passThrough — обычный прокси без кэша: индексы, HEAD, всё остальное.
func (c *Cache) passThrough(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, h := range []string{"If-Modified-Since", "If-None-Match", "Range", "Accept", "User-Agent", "Cache-Control"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		http.Error(w, "nkt apt cache: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Last-Modified", "ETag", "Content-Range", "Accept-Ranges", "Cache-Control", "Expires"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	n, _ := io.Copy(w, resp.Body)
	c.bytesServed.Add(n)
}

// tunnel — CONNECT для https-репозиториев: сквозной TCP.
func (c *Cache) tunnel(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	upstream, err := net.DialTimeout("tcp", r.Host, 15*time.Second)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
	conn, buf, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	go func() {
		defer upstream.Close()
		defer conn.Close()
		if buf.Reader.Buffered() > 0 {
			_, _ = io.CopyN(upstream, buf, int64(buf.Reader.Buffered()))
		}
		_, _ = io.Copy(upstream, conn)
	}()
	_, _ = io.Copy(conn, upstream)
	conn.Close()
	upstream.Close()
}
