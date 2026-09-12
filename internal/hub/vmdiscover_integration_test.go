package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// Машины, созданные не через nkt, хаб находит у хоста и заводит записями с
// родителем: настоящий nkt на фикстурах играет хост с доменами web-vm
// (работает) и db-vm (выключена).
func TestVMDiscoverAndImport(t *testing.T) {
	manager, db, _, hostID := startFixturesHost(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	srv := &Server{hub: manager, db: db, log: slog.New(slog.DiscardHandler)}
	r := chi.NewRouter()
	r.Get("/hub/hosts/{id}/vm-discover", srv.handleVMDiscover)
	r.Post("/hub/hosts/{id}/vm-import", srv.handleVMImport)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hub/hosts/1/vm-discover", nil).WithContext(ctx)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("discover: %d %s", rec.Code, rec.Body.String())
	}
	var found struct {
		VMs []discoveredVM `json:"vms"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &found)
	names := map[string]string{}
	for _, vm := range found.VMs {
		names[vm.Name] = vm.State
	}
	if names["web-vm"] != "running" || names["db-vm"] == "" {
		t.Fatalf("найдено %v, ожидались web-vm (running) и db-vm", names)
	}

	// Импорт без пароля — с ключом хаба; запись получает родителя.
	body := strings.NewReader(`{"names":["db-vm","нет-такой"],"ssh_user":"root"}`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/hub/hosts/1/vm-import", body).WithContext(auth.WithUser(ctx, store.User{Username: "test"}))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Imported []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			PublicKey string `json:"public_key"`
		} `json:"imported"`
		Errors []string `json:"errors"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if len(res.Imported) != 1 || res.Imported[0].Name != "db-vm" || res.Imported[0].PublicKey == "" {
		t.Fatalf("импортировано %+v, ожидалась db-vm с ключом хаба", res.Imported)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "нет-такой") {
		t.Errorf("ошибки = %v, ожидался отказ по несуществующей машине", res.Errors)
	}
	imported, err := db.HostByID(ctx, res.Imported[0].ID)
	if err != nil {
		t.Fatalf("HostByID: %v", err)
	}
	if imported.ParentID != hostID || imported.Addr != PlaceholderAddr {
		t.Errorf("запись = %+v, ожидался родитель %d и адрес-заглушка (машина выключена)", imported, hostID)
	}

	// Повторный поиск её уже не показывает.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/hub/hosts/1/vm-discover", nil).WithContext(ctx)
	r.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &found)
	for _, vm := range found.VMs {
		if vm.Name == "db-vm" {
			t.Errorf("db-vm всё ещё в найденных после импорта")
		}
	}
}
