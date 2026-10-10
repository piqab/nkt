package api

import (
	"errors"
	"reflect"
	"testing"
)

// up упал на скачивании образа: что на хосте — по сверке ID контейнеров с
// тем, что было до up, а не «работает прежний» вслепую.
func TestUpPullState(t *testing.T) {
	before := []composePSEntry{
		{ID: "a1", Name: "videolog-station-1", State: "running", Health: "healthy"},
		{ID: "b1", Name: "videolog-db-1", State: "exited", ExitCode: 1},
		{ID: "c1", Name: "videolog-web-1", State: "running"},
	}
	after := []composePSEntry{
		{ID: "a1", Name: "videolog-station-1", State: "running", Health: "healthy"},
		{ID: "b1", Name: "videolog-db-1", State: "exited", ExitCode: 1},
		{ID: "c2", Name: "videolog-web-1", State: "created"},
		{Name: "videolog-cron-1", State: "running"},
	}
	want := [][2]string{
		{"compose.upPullKeptRunning", "videolog-station-1"},
		{"compose.upPullKeptDown", "videolog-db-1 — exited (1)"},
		{"compose.upPullRecreated", "videolog-web-1 — created"},
		{"compose.upPullState", "videolog-cron-1 — running"},
	}
	if got := upPullState(before, after); !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	// Стек и до выкладки не работал: не «работает прежняя версия».
	down := []composePSEntry{{ID: "a1", Name: "videolog-station-1", State: "exited", ExitCode: 137}}
	if got := upPullState(down, down); len(got) != 1 || got[0][0] != "compose.upPullKeptDown" {
		t.Fatal(got)
	}
	if got := upPullState(nil, nil); len(got) != 1 || got[0][0] != "compose.upPullNoContainers" {
		t.Fatal(got)
	}
	// Причина — как у шага pull; вывод без ошибки registry — не она.
	if composePullCause("Image x Pulling\nError error from registry: unauthorized", nil) == nil {
		t.Fatal("unauthorized не распознан")
	}
	if err := composePullCause("container exited", errors.New("x")); err == nil || err.Error() != "x" {
		t.Fatal(err)
	}
}
