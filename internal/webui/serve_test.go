package webui

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestServeAssetGzip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte("console.log(1)"))
	_ = zw.Close()
	fsys := fstest.MapFS{
		"assets/app.js.gz": {Data: buf.Bytes()},
		"index.html":       {Data: []byte("<html>")},
	}
	// Браузер с gzip — сжатое как есть.
	r := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	r.Header.Set("Accept-Encoding", "gzip, deflate, br")
	w := httptest.NewRecorder()
	if !ServeAsset(w, r, fsys, "assets/app.js", "x") {
		t.Fatal("not served")
	}
	if w.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(w.Body.Bytes(), buf.Bytes()) || w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("gzip client: %v %q", w.Header(), w.Header().Get("Content-Type"))
	}
	// Клиент без gzip — распакованное.
	r = httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	w = httptest.NewRecorder()
	ServeAsset(w, r, fsys, "assets/app.js", "x")
	body, _ := io.ReadAll(w.Body)
	if w.Header().Get("Content-Encoding") != "" || string(body) != "console.log(1)" {
		t.Fatalf("plain client: %v %q", w.Header(), body)
	}
	// Несжатый файл — как раньше; нет ни того, ни другого — false.
	w = httptest.NewRecorder()
	if !ServeAsset(w, httptest.NewRequest(http.MethodGet, "/index.html", nil), fsys, "index.html", "no-cache") {
		t.Fatal("index not served")
	}
	if ServeAsset(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil), fsys, "x", "") {
		t.Fatal("missing file served")
	}
}
