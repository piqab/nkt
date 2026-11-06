package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// check_path — путь, по которому хаб проверяет сайт снаружи (site.check:
// /ui). В базе, созданной до этого поля, колонки нет: Open обязан добавить
// её миграцией, иначе любой запрос к сайтам падает «no such column», а
// SaveSite и ListSites — носить значение туда и обратно, включая возврат
// к пустому пути (корню).
func TestSiteCheckPathMigratesAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	// sites прежней сборки: без check_path (и без остальных поздних колонок).
	const oldSites = `
CREATE TABLE sites (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    domains        TEXT NOT NULL,
    host_id        INTEGER NOT NULL,
    proxy          TEXT NOT NULL,
    stack          TEXT NOT NULL DEFAULT '',
    service        TEXT NOT NULL DEFAULT '',
    container_port INTEGER NOT NULL DEFAULT 0,
    upstream       TEXT NOT NULL DEFAULT '',
    open_firewall  INTEGER NOT NULL DEFAULT 1,
    status         TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    check_json     TEXT NOT NULL DEFAULT '',
    job_id         INTEGER NOT NULL DEFAULT 0,
    author         TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);`
	if _, err := raw.ExecContext(ctx, oldSites); err != nil {
		t.Fatalf("create old sites table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated: %v", err)
	}
	defer db.Close()

	id, err := db.SaveSite(ctx, Site{Domains: []string{"shop.example.org", "www.shop.example.org"},
		HostID: 1, Proxy: "nginx", Stack: "shop", Service: "web", ContainerPort: 80, MaxBody: "1g", CheckPath: "/ui"})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	list, err := db.ListSites(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].CheckPath != "/ui" {
		t.Fatalf("check path lost: %+v", list)
	}
	// Правка окна сайта присылает все поля: путь возвращается к корню.
	s := list[0]
	s.CheckPath = ""
	if _, err := db.SaveSite(ctx, s); err != nil {
		t.Fatalf("save root: %v", err)
	}
	list, err = db.ListSites(ctx)
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	if len(list) != 1 || list[0].CheckPath != "" {
		t.Fatalf("root path not saved: %+v", list)
	}
}
