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
// install. Drafts never count; pre-releases (беты, тег vX.Y.Z-beta) —
// только когда хаб переведён на бета-канал (beta). Релиз установленной
// версии (cur) ищется и среди бет: хаб на бете должен видеть свои заметки
// независимо от канала.
func pickReleases(rels []githubRelease, current string, beta bool) (latest githubRelease, previous string, cur githubRelease) {
	for _, r := range rels {
		if r.Draft {
			continue
		}
		v := strings.TrimPrefix(strings.TrimSpace(r.TagName), "v")
		if _, ok := parseSemver(v); !ok {
			continue
		}
		// Релиз установленной версии: его описание показывается, пока
		// обновления нет, — «что нового в этой версии» полезнее пустого
		// места, а после выхода следующей блок сам переключится на неё.
		if v == current {
			cur = r
		}
		if r.Prerelease && !beta {
			continue
		}
		if latest.TagName == "" || isNewerVersion(v, strings.TrimPrefix(latest.TagName, "v")) {
			latest = r
		}
		if isNewerVersion(current, v) && (previous == "" || isNewerVersion(v, previous)) {
			previous = v
		}
	}
	return latest, previous, cur
}

// betaSuffix — так помечена бета и в теге релиза (vX.Y.Z-beta), и в
// версии бинарника (X.Y.Z-beta): одна строка, чтобы ссылки на GitHub
// (releases/download/v<версия>/…) собирались без ветвлений.
const betaSuffix = "-beta"

// isBetaVersion — версия бета-сборки.
func isBetaVersion(v string) bool { return strings.HasSuffix(strings.TrimSpace(v), betaSuffix) }

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
	beta := m.betaChannelEnabled(ctx)
	rel, previous, cur := pickReleases(rels, m.version, beta)
	latest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	if latest == "" {
		m.recordVersionCheck("", "", "", msgs.Errorf("hub.emptyTagNameGitHubResponse"))
		return
	}
	ru, en := splitReleaseNotes(rel.Body)
	curRU, curEN := splitReleaseNotes(cur.Body)
	m.recordVersionCheck(latest, previous, ru, nil)
	m.versionMu.Lock()
	m.latestNotesEN = en
	m.currentNotes, m.currentNotesEN = curRU, curEN
	m.betaChannel = beta
	m.versionMu.Unlock()
}

// betaChannelKVKey — настройка «использовать бета-версии» в базе хаба.
const betaChannelKVKey = "update.beta"

// betaChannelEnabled читает настройку из базы: она меняется из
// интерфейса, и фоновая проверка должна видеть свежее значение.
func (m *Manager) betaChannelEnabled(ctx context.Context) bool {
	raw, ok, err := m.db.KVGet(ctx, betaChannelKVKey)
	return err == nil && ok && raw == "1"
}

// SetBetaChannel включает или выключает бета-канал и сразу перепроверяет
// версии: «последняя доступная» после переключения должна измениться на
// глазах, а не через шесть часов.
func (m *Manager) SetBetaChannel(ctx context.Context, on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	if err := m.db.KVSet(ctx, betaChannelKVKey, v); err != nil {
		return err
	}
	m.versionMu.Lock()
	m.betaChannel = on
	m.versionMu.Unlock()
	return nil
}

// maxReleaseNotes caps what is kept from a release body. Nothing this
// project publishes comes close, but the text is written on GitHub's side
// and ends up in every /hub/version response — a runaway body should cost a
// truncated panel, not the memory of every hub polling it.
const maxReleaseNotes = 16 << 10

// cleanReleaseNotes normalises a GitHub release body for display. The UI
// renders it as plain text (no markdown renderer, no HTML), so the only
// work here is line endings, trimming and the size cap.
// releaseNotesMarker разделяет в теле релиза русскую и английскую части
// (workflow склеивает WHATSNEW.md и WHATSNEW.en.md через него); без
// маркера всё тело — на одном языке для обоих.
const releaseNotesMarker = "<!-- en -->"

// splitReleaseNotes — русская и английская части тела релиза.
func splitReleaseNotes(body string) (ru, en string) {
	ru, en, found := strings.Cut(body, releaseNotesMarker)
	if !found {
		return cleanReleaseNotes(body), ""
	}
	return cleanReleaseNotes(ru), cleanReleaseNotes(en)
}

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
	//
	// Когда обновления нет, здесь описание установленной версии: «что
	// нового» нужно читать и после обновления, а не только до него.
	// NotesAreCurrent отличает один случай от другого.
	Notes string
	// NotesAreCurrent — Notes описывают уже установленную версию.
	NotesAreCurrent bool
	// Beta — хаб переведён на бета-канал: беты считаются за обновления.
	Beta bool
	// IsBeta — установлена бета-сборка (версия с суффиксом -beta).
	IsBeta bool
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
	return m.VersionStatusFor(msgs.DefaultLang)
}

// VersionStatusFor — то же с описанием релиза на языке lang (английская
// часть есть только у релизов с WHATSNEW.en.md; иначе — русская).
func (m *Manager) VersionStatusFor(lang msgs.Lang) VersionInfo {
	m.versionMu.Lock()
	latest, checkedAt, checkErr := m.latestVersion, m.versionCheckedAt, m.versionCheckErr
	notes, previous := m.latestNotes, m.previousVersion
	if lang == msgs.EN && m.latestNotesEN != "" {
		notes = m.latestNotesEN
	}
	curNotes := m.currentNotes
	if lang == msgs.EN && m.currentNotesEN != "" {
		curNotes = m.currentNotesEN
	}
	beta := m.betaChannel
	m.versionMu.Unlock()

	updateAvailable := latest != "" && isNewerVersion(latest, m.version)
	notesAreCurrent := false
	if !updateAvailable {
		// Обновления нет — показываем описание того, что уже стоит.
		notes, notesAreCurrent = curNotes, curNotes != ""
	}
	return VersionInfo{
		Current:         m.version,
		Latest:          latest,
		UpdateAvailable: updateAvailable,
		Previous:        previous,
		CheckedAt:       checkedAt,
		CheckError:      checkErr,
		Notes:           notes,
		NotesAreCurrent: notesAreCurrent,
		Updatable:       hubSelfUpdateSupported(),
		Beta:            beta,
		IsBeta:          isBetaVersion(m.version),
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
//
// Бета той же версии старше стабильной (1.10.83-beta < 1.10.83), а
// следующая бета новее прошлой стабильной (1.10.84-beta > 1.10.83) —
// обычный порядок pre-release, без него хаб на бете не увидел бы выхода
// той же версии в стабильном виде.
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
	return isBetaVersion(current) && !isBetaVersion(latest)
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
