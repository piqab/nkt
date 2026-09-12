package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.8.42", "1.8.41", true},
		{"1.8.41", "1.8.41", false},
		{"1.8.40", "1.8.41", false},
		{"2.0.0", "1.99.99", true},
		{"1.9.0", "1.8.99", true},
		// The hub itself already newer than whatever "latest" GitHub
		// reports (a dev build ahead of the last tagged release) must
		// never be reported as updatable — that would only downgrade it.
		{"1.8.30", "1.8.42", false},
		// Unparseable strings ("dev" builds, a bare git hash) only ever
		// compare as "differs", never guessed at.
		{"dev", "1.8.41", true},
		{"1.8.41", "dev", true},
		{"dev", "dev", false},
	}
	for _, c := range cases {
		if got := isNewerVersion(c.latest, c.current); got != c.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestVersionStatusReflectsRecordedCheck(t *testing.T) {
	m, _ := newTestManager(t)
	m.version = "1.8.41"

	// Before any check: no latest known, so never "update available" no
	// matter what — a stale cache must never claim an update exists.
	status := m.VersionStatus()
	if status.UpdateAvailable {
		t.Errorf("UpdateAvailable = true before any check ever ran")
	}
	if status.Current != "1.8.41" {
		t.Errorf("Current = %q, want %q", status.Current, "1.8.41")
	}

	m.recordVersionCheck("1.8.42", "1.8.40", "## v1.8.42\n- новый раздел", nil)
	status = m.VersionStatus()
	if status.Notes != "## v1.8.42\n- новый раздел" {
		t.Errorf("Notes = %q, want the recorded release description", status.Notes)
	}
	if !status.UpdateAvailable {
		t.Error("UpdateAvailable = false after recording a newer latest version")
	}
	if status.Latest != "1.8.42" {
		t.Errorf("Latest = %q, want %q", status.Latest, "1.8.42")
	}
	if status.CheckedAt.IsZero() {
		t.Error("CheckedAt still zero after a recorded check")
	}
	if status.CheckError != "" {
		t.Errorf("CheckError = %q, want empty after a successful check", status.CheckError)
	}

	// A subsequent failed check must not throw away the last known-good
	// latest version — only the error/timestamp should change, exactly
	// like recordUnreachable's own doc comment for hostOverview.
	m.recordVersionCheck("", "", "", errNetworkDown)
	status = m.VersionStatus()
	if status.Notes == "" {
		t.Error("Notes cleared by a failed check — the last known-good description must survive it, like Latest")
	}
	if status.Latest != "1.8.42" {
		t.Errorf("Latest = %q after a failed check, want the last known-good %q preserved", status.Latest, "1.8.42")
	}
	if status.CheckError == "" {
		t.Error("CheckError empty after recording a failed check")
	}
}

// errNetworkDown is a stand-in error for TestVersionStatusReflectsRecordedCheck.
var errNetworkDown = &testError{"network down"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestApplyUpdateRefusesWithoutAKnownNewerVersion(t *testing.T) {
	m, _ := newTestManager(t)
	m.version = "1.8.41"

	if err := m.ApplyUpdate(context.Background()); err == nil {
		t.Fatal("ApplyUpdate accepted with no version ever checked")
	}

	// Even with a check recorded, refuse when it isn't actually newer —
	// applying it would be a no-op at best, a downgrade at worst if the
	// hub is ahead of GitHub's latest tag (a dev build).
	m.recordVersionCheck("1.8.41", "", "", nil)
	if err := m.ApplyUpdate(context.Background()); err == nil {
		t.Fatal("ApplyUpdate accepted when latest == current")
	}
}

// TestCheckLatestVersionLive hits the real GitHub API — the one thing the
// hermetic tests above cannot cover. Skipped unless
// NKT_TEST_LIVE_VERSION_CHECK=1; run it by hand after touching
// checkLatestVersion/githubLatestRelease.
func TestCheckLatestVersionLive(t *testing.T) {
	if os.Getenv("NKT_TEST_LIVE_VERSION_CHECK") != "1" {
		t.Skip("set NKT_TEST_LIVE_VERSION_CHECK=1 to run (hits the real GitHub API)")
	}

	m, _ := newTestManager(t)
	m.cfg.HubReleaseRepo = "piqab/nkt"
	m.version = "0.0.0" // guarantees UpdateAvailable=true regardless of what's actually latest

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status := m.CheckNow(ctx)

	if status.CheckError != "" {
		t.Fatalf("CheckNow reported an error: %s", status.CheckError)
	}
	if status.Latest == "" {
		t.Fatal("CheckNow found no latest version")
	}
	if !status.UpdateAvailable {
		t.Errorf("UpdateAvailable = false with Current=0.0.0 and Latest=%q", status.Latest)
	}
	t.Logf("latest release: v%s", status.Latest)
	// The "что нового" panel has nothing to show if GitHub's body never
	// makes it through the decoder — the one thing only a live response can
	// prove, since every release this project publishes carries one.
	if status.Notes == "" {
		t.Error("Notes empty — the release description did not survive decoding")
	}
	t.Logf("release notes: %.200s", status.Notes)
}

// cleanReleaseNotes runs on text written outside this repository (a release
// body can be edited on GitHub), so the cap has to hold on multi-byte text:
// the notes are Russian, and a cut mid-rune would surface as a replacement
// character in the UI.
func TestCleanReleaseNotes(t *testing.T) {
	if got := cleanReleaseNotes("  ## v1.9.0\r\n- строка\r\n\n"); got != "## v1.9.0\n- строка" {
		t.Errorf("cleanReleaseNotes = %q, want CRLF normalised and trimmed", got)
	}
	if got := cleanReleaseNotes(""); got != "" {
		t.Errorf("cleanReleaseNotes(\"\") = %q, want empty", got)
	}

	long := strings.Repeat("я", maxReleaseNotes) // two bytes per rune: well over the cap
	got := cleanReleaseNotes(long)
	if len(got) > maxReleaseNotes+len("\n…") {
		t.Errorf("len = %d, want at most the cap plus the ellipsis", len(got))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncated notes must say so")
	}
	if !utf8.ValidString(got) {
		t.Error("truncation left invalid UTF-8 — a cut mid-rune")
	}
}

// Из списка релизов берётся самый новый опубликованный (черновики и
// пре-релизы не считаются) и ближайший ниже текущего — цель отката.
func TestPickReleases(t *testing.T) {
	rels := []githubRelease{
		{TagName: "v1.9.64", Draft: true},
		{TagName: "v1.9.65", Prerelease: true},
		{TagName: "v1.9.63", Body: "latest"},
		{TagName: "v1.9.61"},
		{TagName: "v1.9.62"},
		{TagName: "v1.9.60"},
		{TagName: "garbage"},
	}
	latest, prev := pickReleases(rels, "1.9.63")
	if latest.TagName != "v1.9.63" || prev != "1.9.62" {
		t.Errorf("current 1.9.63: latest=%s prev=%s", latest.TagName, prev)
	}
	_, prev = pickReleases(rels, "1.9.62")
	if prev != "1.9.61" {
		t.Errorf("current 1.9.62: prev=%s", prev)
	}
	_, prev = pickReleases(rels, "1.9.60")
	if prev != "" {
		t.Errorf("самая старая версия: prev=%s, ожидалось пусто", prev)
	}
}

// Проверка версии ходит за списком релизов; предыдущая версия попадает
// в статус и в ответ API, откат без неё отказывает.
func TestCheckLatestVersionListsReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/piqab/nkt/releases" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v1.9.63","body":"## v1.9.63\n- x"},{"tag_name":"v1.9.62","body":""},{"tag_name":"v1.9.61"}]`))
	}))
	defer srv.Close()
	m, _ := newTestManager(t)
	m.cfg.HubReleaseRepo = "piqab/nkt"
	m.cfg.HubGitHubAPI = srv.URL
	m.version = "1.9.62"
	st := m.CheckNow(context.Background())
	if st.CheckError != "" || st.Latest != "1.9.63" || !st.UpdateAvailable || st.Previous != "1.9.61" {
		t.Fatalf("статус: %+v", st)
	}
	if got := versionInfoJSON(st)["previous"]; got != "1.9.61" {
		t.Errorf("previous в JSON = %v", got)
	}
	m.version = "1.9.61"
	st = m.CheckNow(context.Background())
	if st.Previous != "" {
		t.Errorf("у самой старой версии prev = %q", st.Previous)
	}
	if err := m.Rollback(context.Background()); err == nil {
		t.Error("откат без предыдущей версии прошёл")
	}
}
