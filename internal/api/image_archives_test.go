package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/store"
)

// Архивы образов: загрузка потоком, список по движку, скачивание,
// удаление; имена с путём и чужие расширения не принимаются.
func TestImageArchives(t *testing.T) {
	s, _, _ := f2bServer(t)
	dir := t.TempDir()
	s.images = control.NewImageManager(s.scanner.Collector(), s.scanner, dir)
	ctx := auth.WithUser(context.Background(), store.User{Username: "admin", Role: store.RoleAdmin})
	withName := func(r *http.Request, name string) *http.Request {
		rc := chi.NewRouteContext()
		rc.URLParams.Add("name", name)
		return r.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rc))
	}
	put := func(name, body string) int {
		w := httptest.NewRecorder()
		s.handleImageArchiveUpload(w, httptest.NewRequest(http.MethodPut, "/?name="+name, strings.NewReader(body)).WithContext(ctx))
		return w.Code
	}
	if c := put("podman__app.tar", "IMAGE"); c != http.StatusOK {
		t.Fatalf("upload: %d", c)
	}
	for _, bad := range []string{"../x.tar", "a/b.tar", "x.sh", ".hidden.tar"} {
		if c := put(bad, "x"); c != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", bad, c)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "podman__app.tar")); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleImageArchives(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	var out struct {
		Archives []struct{ Name, Kind string }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Archives) != 1 || out.Archives[0].Kind != "podman" {
		t.Fatalf("list: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	s.handleImageArchiveDownload(w, withName(httptest.NewRequest(http.MethodGet, "/", nil), "podman__app.tar"))
	if w.Body.String() != "IMAGE" || !strings.Contains(w.Header().Get("Content-Disposition"), "podman__app.tar") {
		t.Fatalf("download: %q %v", w.Body.String(), w.Header())
	}
	w = httptest.NewRecorder()
	s.handleImageArchiveDownload(w, withName(httptest.NewRequest(http.MethodGet, "/", nil), "../../etc/passwd"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("traversal: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleImageArchiveDelete(w, withName(httptest.NewRequest(http.MethodDelete, "/", nil), "podman__app.tar"))
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "podman__app.tar")); !os.IsNotExist(err) {
		t.Fatal("not deleted")
	}
	// Сохранение и загрузка в снимке не выполняются (команды движка).
	w = httptest.NewRecorder()
	s.handleImageArchiveSave(w, httptest.NewRequest(http.MethodPost, "/?engine=docker&ref=nginx:1", nil).WithContext(ctx))
	if w.Code != http.StatusForbidden {
		t.Fatalf("save in fixtures: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.handleImageArchiveSave(w, httptest.NewRequest(http.MethodPost, "/?engine=docker&ref=$(id)", nil).WithContext(ctx))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad ref: %d", w.Code)
	}
}
