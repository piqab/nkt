package deploy

import (
	"reflect"
	"testing"
)

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"IMG": "rgstr.example.com/test/station:latest", "EMPTY": "", "REG": "rgstr.example.com"}
	cases := map[string]string{
		"${IMG:-ghcr.io/o/s:latest}":  "rgstr.example.com/test/station:latest",
		"${NOPE:-ghcr.io/o/s:latest}": "ghcr.io/o/s:latest",
		"${EMPTY:-def}":               "def",
		"${EMPTY-def}":                "",
		"${REG}/test/station:1":       "rgstr.example.com/test/station:1",
		"$REG/x":                      "rgstr.example.com/x",
		"${NOPE}/test/station":        "/test/station",
		"${NOPE:-${REG}/a}":           "rgstr.example.com/a",
		"${REG:+set}${NOPE:+unset}":   "set",
		"${IMG:?set IMG}":             "rgstr.example.com/test/station:latest",
		"cost $$5":                    "cost $5",
		"plain/image:tag":             "plain/image:tag",
	}
	for in, want := range cases {
		if got := ExpandVars(in, vars); got != want {
			t.Errorf("%q → %q, ждали %q", in, got, want)
		}
	}
}

func TestComposeImagesEnv(t *testing.T) {
	text := "services:\n  station:\n    image: ${VIDEOLOG_STATION_IMAGE:-ghcr.io/piqab/videolog-station:latest}\n  db:\n    image: postgres:16\n"
	got := ComposeImagesEnv(text, map[string]string{"VIDEOLOG_STATION_IMAGE": "rgstr.example.com/test/videolog/videolog-station:latest"})
	want := []string{"postgres:16", "rgstr.example.com/test/videolog/videolog-station:latest"}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if got := ComposeImagesEnv(text, nil); got[0] != "ghcr.io/piqab/videolog-station:latest" {
		t.Fatal(got)
	}
}

func TestImageRefProblem(t *testing.T) {
	cases := map[string]string{
		"rgstr.example.com/test/videolog/videolog-station:latest": "",
		"registry.example.com:5000/a/b:1.2":                       "",
		"postgres:16":                                             "",
		"ghcr.io/o/app@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": "",
		"localhost:5000/app":                           "",
		"http://rgstr.example.com/test/station:latest": ImageRefScheme,
		"HTTPS://rgstr.example.com/test/station":       ImageRefScheme,
		"/test/videolog/videolog-station:latest":       ImageRefNoHost,
		"rgstr.example.com/Test/Station":               ImageRefBad,
		"rgstr.example.com//station":                   ImageRefBad,
		"":                                             ImageRefBad,
	}
	for in, want := range cases {
		if got := ImageRefProblem(in); got != want {
			t.Errorf("%q → %q, ждали %q", in, got, want)
		}
	}
}
