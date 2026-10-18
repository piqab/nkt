package webui

import (
	"compress/gzip"
	"io"
	"io/fs"
	"mime"
	"net/http"
	gopath "path"
	"strings"
)

// Крупные ресурсы сборки (JS, CSS, SVG) лежат в бинарнике сжатыми — файл
// «имя.gz» вместо «имя» (web/scripts/gzip-dist.mjs): так бинарник на
// несколько мегабайт меньше. Браузеру они уходят как есть, с
// Content-Encoding: gzip; клиенту без gzip — распакованными на лету.

// ServeAsset отдаёт path из fsys, если он там есть сам или сжатым; false —
// нет ни того, ни другого. cache — Cache-Control.
func ServeAsset(w http.ResponseWriter, r *http.Request, fsys fs.FS, path, cache string) bool {
	if f, err := fsys.Open(path); err == nil {
		_ = f.Close()
		w.Header().Set("Cache-Control", cache)
		http.FileServer(http.FS(fsys)).ServeHTTP(w, r)
		return true
	}
	gz, err := fs.ReadFile(fsys, path+".gz")
	if err != nil {
		return false
	}
	if ct := mime.TypeByExtension(gopath.Ext(path)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", cache)
	w.Header().Add("Vary", "Accept-Encoding")
	if acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz)
		return true
	}
	zr, err := gzip.NewReader(strings.NewReader(string(gz)))
	if err != nil {
		http.Error(w, "broken asset", http.StatusInternalServerError)
		return true
	}
	// Ресурсы свои, из бинарника, но распаковка всё равно с потолком —
	// чтобы испорченный архив не раздулся без меры.
	if _, err := io.CopyN(w, zr, maxAssetSize); err != nil && err != io.EOF {
		return true
	}
	return true
}

// maxAssetSize — больше этого ресурс интерфейса распакованным не бывает.
const maxAssetSize = 64 << 20

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") && strings.TrimSpace(q) != "q=0" {
			return true
		}
	}
	return false
}
