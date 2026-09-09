package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/config"
)

func TestAptPackageNameRe(t *testing.T) {
	valid := []string{"htop", "python3", "libssl-dev", "g++", "lib32z1", "a"}
	for _, name := range valid {
		if !aptPackageNameRe.MatchString(name) {
			t.Errorf("aptPackageNameRe rejected valid name %q", name)
		}
	}

	invalid := []string{"", "Htop", "-htop", ".htop", "htop; rm -rf /", "htop && echo pwned", "htop$(id)", "htop`id`", "htop foo"}
	for _, name := range invalid {
		if aptPackageNameRe.MatchString(name) {
			t.Errorf("aptPackageNameRe accepted invalid name %q", name)
		}
	}
}

// TestParseAptCacheSearchSplitsOnFirstSeparatorOnly locks in the fix for a
// real parsing hazard: some real package descriptions contain " - " again
// later in the text (mtr-tiny's is the textbook example), which a
// last-occurrence split or an unbounded Split would truncate.
func TestParseAptCacheSearchSplitsOnFirstSeparatorOnly(t *testing.T) {
	stdout := "htop - interactive processes viewer\n" +
		"mtr-tiny - Full screen ping and traceroute tool - non-gui version\n" +
		"nodesc\n" +
		"\n"

	got := parseAptCacheSearch(stdout)
	want := map[string]string{
		"htop":     "interactive processes viewer",
		"mtr-tiny": "Full screen ping and traceroute tool - non-gui version",
		"nodesc":   "",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d: %+v", len(got), len(want), got)
	}
	for _, r := range got {
		if desc, ok := want[r.Name]; !ok || desc != r.Description {
			t.Errorf("result %+v, want description %q", r, want[r.Name])
		}
	}
}

func TestParseAptCacheSearchSortsByName(t *testing.T) {
	stdout := "zsh - shell\nbash - shell\nawk - text processor\n"
	got := parseAptCacheSearch(stdout)
	want := []string{"awk", "bash", "zsh"}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("got[%d].Name = %q, want %q", i, got[i].Name, name)
		}
	}
}

// TestParseDpkgQueryVersionsSkipsNonInstalled confirms the last-field
// status check (not a substring match) and the residual-package exclusion
// both hold for the full-inventory listing, the same way
// TestParseDpkgQueryOutput already locks it in for the curated one.
func TestParseDpkgQueryVersionsSkipsNonInstalled(t *testing.T) {
	stdout := "htop\t3.2.2-2\tinstall ok installed\n" +
		"old-pkg\t1.0\tdeinstall ok config-files\n" +
		"never-installed\t\tunknown ok not-installed\n" +
		"malformed line without tabs\n"

	got := parseDpkgQueryVersions(stdout)
	if len(got) != 1 {
		t.Fatalf("got %d packages, want 1: %+v", len(got), got)
	}
	if got[0].Name != "htop" || got[0].Version != "3.2.2-2" {
		t.Errorf("got %+v, want {htop 3.2.2-2}", got[0])
	}
}

func TestHandleAptInstallWSGates(t *testing.T) {
	t.Run("refused for an invalid package name", func(t *testing.T) {
		s := newTestServer(t, &config.Config{Mode: config.ModeLocal})
		r := chi.NewRouter()
		r.Get("/{name}", s.handleAptInstallWS)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/htop%3Brm", nil)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("refused in fixtures mode", func(t *testing.T) {
		s := newTestServer(t, &config.Config{Mode: config.ModeFixtures})
		r := chi.NewRouter()
		r.Get("/{name}", s.handleAptInstallWS)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/htop", nil)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}

func TestHandleAptRemoveWSGates(t *testing.T) {
	t.Run("refused for an invalid package name", func(t *testing.T) {
		s := newTestServer(t, &config.Config{Mode: config.ModeLocal})
		r := chi.NewRouter()
		r.Get("/{name}", s.handleAptRemoveWS)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/Htop", nil)
		r.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})
}

func TestHandleAptSearchRequiresMinQueryLength(t *testing.T) {
	s := newTestServer(t, &config.Config{Mode: config.ModeFixtures})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system/apt/search?q=a", nil)
	s.handleAptSearch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Results []aptSearchResult `json:"results"`
	}
	decodeJSONBody(t, rec, &body)
	if len(body.Results) != 0 {
		t.Errorf("results = %+v, want empty for a 1-char query", body.Results)
	}
}

// Строки взяты из настоящего `apt-cache search htop` на Debian: он ищет и
// по имени, и по описанию, и отдаёт всё вперемешку по алфавиту — из-за
// чего сам htop оказывался в середине выдачи, а на длинном запросе мог
// вообще не попасть в лимит.
func TestRankAptResults(t *testing.T) {
	raw := []aptSearchResult{
		{Name: "aptitude", Description: "terminal-based package manager, like htop for packages"},
		{Name: "glances", Description: "Curses-based monitoring tool, an htop alternative"},
		{Name: "htop", Description: "interactive processes viewer"},
		{Name: "htop-vim", Description: "interactive processes viewer with vim keybindings"},
		{Name: "libhtop0", Description: "library used by htop"},
		{Name: "pcp-htop", Description: "htop-like tool for Performance Co-Pilot"},
	}

	got := rankAptResults(raw, "htop")

	wantOrder := []string{"htop", "htop-vim", "libhtop0", "pcp-htop", "aptitude", "glances"}
	for i, want := range wantOrder {
		if got[i].Name != want {
			t.Errorf("позиция %d: %q, ожидалось %q (полный порядок: %v)", i, got[i].Name, want, names(got))
		}
	}

	wantMatch := map[string]string{
		"htop":     aptMatchExact,
		"htop-vim": aptMatchPrefix,
		"libhtop0": aptMatchName,
		"pcp-htop": aptMatchName,
		"aptitude": aptMatchDescription,
		"glances":  aptMatchDescription,
	}
	for _, r := range got {
		if r.Match != wantMatch[r.Name] {
			t.Errorf("%s отнесён к группе %q, ожидалась %q", r.Name, r.Match, wantMatch[r.Name])
		}
	}
}

// Регистр в именах пакетов встречается редко, но запрос пользователь
// набирает как придётся.
func TestClassifyAptMatchIgnoresCase(t *testing.T) {
	if got := classifyAptMatch("htop", "HTOP"); got != aptMatchExact {
		t.Errorf("classifyAptMatch(htop, HTOP) = %q", got)
	}
	if got := classifyAptMatch("HTop-Vim", "htop"); got != aptMatchPrefix {
		t.Errorf("classifyAptMatch(HTop-Vim, htop) = %q", got)
	}
}

// Внутри группы порядок остаётся алфавитным — тем самым, в котором apt и
// отдаёт результаты.
func TestRankAptResultsKeepsAlphabeticalWithinGroup(t *testing.T) {
	got := rankAptResults([]aptSearchResult{
		{Name: "nginx-full"}, {Name: "nginx"}, {Name: "nginx-common"},
	}, "nginx")
	want := []string{"nginx", "nginx-common", "nginx-full"}
	for i := range want {
		if got[i].Name != want[i] {
			t.Fatalf("порядок %v, ожидался %v", names(got), want)
		}
	}
}

func names(results []aptSearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Name
	}
	return out
}
