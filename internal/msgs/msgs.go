// Package msgs is nkt's own message catalog for backend-authored
// user-facing text — the Go-side counterpart to the frontend's
// react-i18next catalog (web/src/i18n/{ru,en}.json). react-i18next only
// covers static frontend strings; text the Go backend itself generates
// (API error messages, install/renewal progress lines, finding
// diagnostics) needs its own lookup, keyed the same way and selected by
// the request's language instead of the browser's. Raw output from
// external tools (nginx -t, certbot, apt-get, ...) is never a target here
// — that text isn't ours to translate and stays as-is wherever it
// surfaces.
package msgs

import (
	"fmt"
	"net/http"
)

type Lang string

const (
	RU Lang = "ru"
	EN Lang = "en"
)

// DefaultLang matches the frontend's own i18next fallbackLng.
const DefaultLang = RU

var catalogs = map[Lang]map[string]string{
	RU: ruCatalog,
	EN: enCatalog,
}

// T looks up key in lang's catalog, falling back to DefaultLang if the key
// is missing there, and to the bare key itself if it's missing from
// DefaultLang too — the same two-step fallback the frontend's i18next
// config uses, and for the same reason: an untranslated key should be
// visibly wrong during development, not silently blank.
func T(lang Lang, key string, args ...any) string {
	tmpl, ok := catalogs[lang][key]
	if !ok {
		tmpl, ok = catalogs[DefaultLang][key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return tmpl
	}
	return fmt.Sprintf(tmpl, args...)
}

// Err carries a catalog key (plus its format args) instead of a fixed
// string, so the same error can render in whichever language the request
// that ultimately reports it is in. Error() always renders DefaultLang —
// any code that just calls .Error() (logs, %w-wrapping elsewhere) keeps
// seeing today's Russian text unchanged; only the API boundary (fail, see
// internal/api/server.go and internal/hub/handlers.go) unwraps via
// errors.As to localize against the actual request.
type Err struct {
	Key  string
	Args []any
}

func (e *Err) Error() string {
	return T(DefaultLang, e.Key, e.Args...)
}

// Errorf builds an *Err — the msgs-catalog equivalent of fmt.Errorf, for a
// leaf error (never further wrapped before reaching the API boundary) that
// should localize against the request's language instead of always
// rendering Russian.
func Errorf(key string, args ...any) error {
	return &Err{Key: key, Args: args}
}

// langHeader is set by every web/src/api.ts call, straight from
// i18next's current language — see App.tsx's useLang/getStoredLang.
const langHeader = "X-NKT-Lang"

// langQueryParam is the WebSocket-upgrade equivalent of langHeader: browser
// JS cannot set custom headers on a WebSocket handshake, so wsURL() in
// api.ts appends this instead. Plain HTTP requests can carry either; the
// header wins if somehow both are present.
const langQueryParam = "lang"

// ParseLang validates a raw language tag (from a header or query param)
// against the catalog's known languages, defaulting to DefaultLang for
// anything unset or unrecognized — mirrors the frontend's own
// getStoredLang() fallback.
func ParseLang(raw string) Lang {
	switch Lang(raw) {
	case EN:
		return EN
	case RU:
		return RU
	default:
		return DefaultLang
	}
}

// LangFromRequest reads the request's language from the X-NKT-Lang header
// or, for WebSocket upgrades, the "lang" query parameter. The hub's own
// reverse proxy to a managed host (internal/hub/proxy.go) forwards
// incoming headers unmodified, so this reads correctly whether the request
// reached this process directly or through the hub's proxy.
func LangFromRequest(r *http.Request) Lang {
	if h := r.Header.Get(langHeader); h != "" {
		return ParseLang(h)
	}
	return ParseLang(r.URL.Query().Get(langQueryParam))
}
