package store

import (
	"context"
	"path/filepath"
	"testing"
)

// «Задания»: поиск по тексту (и в аргументах названия), фильтр по
// состоянию и виду, страницы и порядок по дате.
func TestFindJobs(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	mk := func(kind, title, args, status string) int64 {
		id, err := db.CreateJob(ctx, Job{Kind: kind, Title: title, TitleArgs: args, Queue: "q", Author: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		if status != "" {
			if err := db.FinishJob(ctx, id, status, "boom 100%"); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	a := mk("deploy.run", "Выкладка shop", `["shop"]`, JobSucceeded)
	mk("fail2ban.fleet", "Бан 203.0.113.9", `["203.0.113.9",4]`, JobFailed)
	mk("deploy.run", "Выкладка blog", `["blog"]`, "")
	c := mk("host.install", "Установка nkt на web-1", `["web-1"]`, JobFailed)

	list, total, err := db.FindJobs(ctx, JobFilter{})
	if err != nil || total != 4 || len(list) != 4 || list[0].ID != c {
		t.Fatalf("all: total %d, first %+v, %v", total, list, err)
	}
	if list, total, _ = db.FindJobs(ctx, JobFilter{Asc: true, Limit: 2}); total != 4 || len(list) != 2 || list[0].ID != a {
		t.Fatalf("asc page: %d %+v", total, list)
	}
	if list, _, _ = db.FindJobs(ctx, JobFilter{Limit: 2, Offset: 2}); len(list) != 2 || list[1].ID != a {
		t.Fatalf("second page: %+v", list)
	}
	if _, total, _ = db.FindJobs(ctx, JobFilter{Kinds: []string{"deploy.run"}}); total != 2 {
		t.Fatalf("kind: %d", total)
	}
	if _, total, _ = db.FindJobs(ctx, JobFilter{Statuses: []string{JobFailed}}); total != 2 {
		t.Fatalf("status: %d", total)
	}
	if list, total, _ = db.FindJobs(ctx, JobFilter{Q: "web-1"}); total != 1 || list[0].ID != c {
		t.Fatalf("q in args: %d %+v", total, list)
	}
	// % в запросе — буквально, а не «что угодно».
	if _, total, _ = db.FindJobs(ctx, JobFilter{Q: "100%"}); total != 3 {
		t.Fatalf("q with %%: %d", total)
	}
	if _, total, _ = db.FindJobs(ctx, JobFilter{Q: "%"}); total != 3 {
		t.Fatalf("literal %%: %d", total)
	}
	kinds, _ := db.JobKinds(ctx)
	if len(kinds) != 3 || kinds[0] != "deploy.run" {
		t.Fatalf("kinds: %v", kinds)
	}
}
