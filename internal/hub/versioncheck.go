package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/api"
)

// versionCheckTimeout bounds a single GitHub API call — this must never be
// allowed to stall the background loop indefinitely on a hung connection.
const versionCheckTimeout = 15 * time.Second

// versionCheckLoop periodically checks HubReleaseRepo's GitHub Releases for
// a version newer than this hub's own, following pollOverviews' exact
// ticker+select shape. Purely informational — see HubUpdateCheckInterval's
// own doc comment — nothing here ever applies an update by itself.
func (m *Manager) versionCheckLoop(ctx context.Context) {
	interval := m.cfg.HubUpdateCheckInterval
	if interval <= 0 {
		return
	}
	// An initial check shortly after startup, not immediately: nothing about
	// this is urgent enough to compete with everything else Run's other
	// goroutines are doing in the first seconds of the process's life.
	initial := time.NewTimer(30 * time.Second)
	defer initial.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initial.C:
		m.checkLatestVersion(ctx)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkLatestVersion(ctx)
		}
	}
}

// githubRelease is the handful of fields this cares about from GitHub's
// "list releases" API response — everything else in the real payload
// (assets, author, ...) is ignored by encoding/json automatically.
type githubRelease struct {
	TagName string `json:"tag_name"`
	// Body is the release description — built from WHATSNEW.md by
	// .github/workflows/release.yml. It is the only way "О системе" can say
	// what a version brings *before* it is installed: the notes shipped
	// inside the running binary describe the version already running.
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// pickReleases picks, out of a release list, the newest published version
// and the one right below current — what "обновить" and "откатить" would
// install. Drafts and pre-releases never count.
func pickReleases(rels []githubRelease, current string) (latest githubRelease, previous string) {
	for _, r := range rels {
		if r.Draft || r.Prerelease {
			continue
		}
		v := strings.TrimPrefix(strings.TrimSpace(r.TagName), "v")
		if _, ok := parseSemver(v); !ok {
			continue
		}
		if latest.TagName == "" || isNewerVersion(v, strings.TrimPrefix(latest.TagName, "v")) {
			latest = r
		}
		if isNewerVersion(current, v) && (previous == "" || isNewerVersion(v, previous)) {
			previous = v
		}
	}
	return latest, previous
}

// checkLatestVersion asks GitHub's public, unauthenticated Releases API for
// HubReleaseRepo's latest tag and records the result — success or failure —
// for VersionStatus to report. Never returns an error itself: this is a
// background check whose only observable effect is updating cached state,
// exactly like pollHost's own "record success or failure, never panic"
// shape.
func (m *Manager) checkLatestVersion(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, versionCheckTimeout)
	defer cancel()

	// The whole list, not /releases/latest: the rollback target — the
	// release right below the running version — is only visible here.
	base := m.cfg.HubGitHubAPI
	if base == "" {
		base = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=50", base, m.cfg.HubReleaseRepo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		m.recordVersionCheck("", "", "", err)
		return
	}
	// GitHub's REST API rejects requests with no Accept header on some
	// endpoints and always prefers this one when present — costs nothing to
	// set explicitly rather than relying on default behavior holding.
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		m.recordVersionCheck("", "", "", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		m.recordVersionCheck("", "", "", msgs.Errorf("hub.githubAPIReturnedCode", resp.StatusCode))
		return
	}

	var rels []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		m.recordVersionCheck("", "", "", err)
		return
	}
	rel, previous := pickReleases(rels, m.version)
	latest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	if latest == "" {
		m.recordVersionCheck("", "", "", msgs.Errorf("hub.emptyTagNameGitHubResponse"))
		return
	}
	m.recordVersionCheck(latest, previous, cleanReleaseNotes(rel.Body), nil)
}

// maxReleaseNotes caps what is kept from a release body. Nothing this
// project publishes comes close, but the text is written on GitHub's side
// and ends up in every /hub/version response — a runaway body should cost a
// truncated panel, not the memory of every hub polling it.
const maxReleaseNotes = 16 << 10

// cleanReleaseNotes normalises a GitHub release body for display. The UI
// renders it as plain text (no markdown renderer, no HTML), so the only
// work here is line endings, trimming and the size cap.
func cleanReleaseNotes(body string) string {
	notes := strings.ReplaceAll(body, "\r\n", "\n")
	notes = strings.TrimSpace(notes)
	if len(notes) > maxReleaseNotes {
		// Cut on a rune boundary: the text is Russian, and half a rune would
		// reach the UI as a replacement character.
		notes = strings.ToValidUTF8(notes[:maxReleaseNotes], "")
		notes = strings.TrimSpace(notes) + "\n…"
	}
	return notes
}

func (m *Manager) recordVersionCheck(latest, previous, notes string, err error) {
	m.versionMu.Lock()
	defer m.versionMu.Unlock()
	m.versionCheckedAt = time.Now()
	if err != nil {
		m.versionCheckErr = err.Error()
		return
	}
	m.latestVersion = latest
	m.previousVersion = previous
	m.latestNotes = notes
	m.versionCheckErr = ""
}

// VersionInfo is the hub's own update-availability status, as handed to
// callers outside this package — mirrors HostOverview's shape (a struct,
// since every field is optional context about the same cached check).
type VersionInfo struct {
	Current         string
	Latest          string
	UpdateAvailable bool
	// Previous is the release right below Current — what "откатить" would
	// install. Empty until a check succeeds, or when Current is the oldest.
	Previous   string
	CheckedAt  time.Time
	CheckError string
	// Notes is the latest release's description — shown by "О системе" when
	// UpdateAvailable, so an operator reads what an update brings before
	// deciding to apply it. Empty when the check has never succeeded, or
	// when the release itself carries no description.
	Notes string
	// Updatable reports whether applyHubUpdate has any real way to install
	// a downloaded binary back onto this machine at all — false for a
	// Docker/Kubernetes-deployed hub (no writable, persistent binary path;
	// see Dockerfile.hub/deploy/docker-compose.hub.yml) or a plain
	// interactive run/test, where the answer is "redeploy the container
	// image" or "rebuild by hand", never a self-update.
	Updatable bool
}

// VersionStatus returns the hub's own version alongside whatever
// versionCheckLoop last learned about the latest release — never triggers a
// fresh check itself (see CheckNow for that).
func (m *Manager) VersionStatus() VersionInfo {
	m.versionMu.Lock()
	latest, checkedAt, checkErr := m.latestVersion, m.versionCheckedAt, m.versionCheckErr
	notes, previous := m.latestNotes, m.previousVersion
	m.versionMu.Unlock()

	return VersionInfo{
		Current:         m.version,
		Latest:          latest,
		UpdateAvailable: latest != "" && isNewerVersion(latest, m.version),
		Previous:        previous,
		CheckedAt:       checkedAt,
		CheckError:      checkErr,
		Notes:           notes,
		Updatable:       hubSelfUpdateSupported(),
	}
}

// CheckNow runs checkLatestVersion synchronously and returns the resulting
// status — the "проверить снова" button's own request, distinct from
// versionCheckLoop's periodic background ticks but sharing every bit of
// logic and cached state with them.
func (m *Manager) CheckNow(ctx context.Context) VersionInfo {
	m.checkLatestVersion(ctx)
	return m.VersionStatus()
}

// semverRe parses a leading MAJOR.MINOR.PATCH off a version string, ignoring
// any suffix — mirrors web/src/pages/Hosts.tsx's own parseSemver exactly,
// so the hub's own "update available" verdict and a managed host's
// "outdated" verdict never disagree about what counts as newer for the
// same two version strings.
var semverRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

func parseSemver(v string) ([3]int, bool) {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

// isNewerVersion reports whether latest is a newer release than current —
// mirrors web/src/pages/Hosts.tsx's isOlderVersion(current, latest), just
// named from the opposite side (this is what VersionStatus's
// UpdateAvailable actually asks). Falls back to a plain "not equal" when
// either side doesn't parse as semver, same as the frontend: the best that
// can be said about an opaque string like "dev" is that it differs, never a
// guess at which of two incomparable strings is "newer".
func isNewerVersion(latest, current string) bool {
	a, aok := parseSemver(latest)
	b, bok := parseSemver(current)
	if !aok || !bok {
		return latest != current
	}
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// hubSelfUpdateSupported reports whether this process is running in a way a
// self-update could actually install itself back onto — i.e. as its own
// systemd unit, with a real escape route out of ProtectSystem=strict to
// write /usr/local/bin and /etc/systemd/system (see
// api.SandboxEscapeAvailable). false in Docker/Kubernetes (the binary lives
// inside the image layer, not a persistent writable path — see
// Dockerfile.hub) and in any plain interactive run or test.
func hubSelfUpdateSupported() bool {
	return api.SandboxEscapeAvailable()
}
