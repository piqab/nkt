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
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
	return e.In(DefaultLang)
}

// In renders the error in lang. Args that are themselves errors are
// localized first, so a chain of *Err values (an outer "запуск git: %v"
// around an inner catalog error) renders wholly in one language.
func (e *Err) In(lang Lang) string {
	if len(e.Args) == 0 {
		return T(lang, e.Key)
	}
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		if err, ok := a.(error); ok {
			args[i] = Localize(lang, err)
		} else {
			args[i] = a
		}
	}
	return T(lang, e.Key, args...)
}

// Unwrap exposes the first error argument, so errors.Is/As keep working
// through msgs.Errorf("...: %v", err) the way they did through %w.
func (e *Err) Unwrap() error {
	for _, a := range e.Args {
		if err, ok := a.(error); ok {
			return err
		}
	}
	return nil
}

// Localize renders err in lang: a *Err (or one wrapped by fmt.Errorf
// somewhere up the chain) gets its catalog text; anything else is shown
// as is. For a wrapped one only the *Err part is re-rendered — the Russian
// prefix around it is replaced in place, so a not-yet-converted wrapper
// degrades to mixed text rather than hiding the localized core.
func Localize(lang Lang, err error) string {
	if err == nil {
		return ""
	}
	var e *Err
	if !errors.As(err, &e) {
		return err.Error()
	}
	if e == err {
		return e.In(lang)
	}
	return strings.Replace(err.Error(), e.Error(), e.In(lang), 1)
}

type ctxKey struct{}

// WithLang stores the request's language in ctx, so code deep below the
// handler (managers building notes and labels, job runners writing their
// log) can render text in it without threading a lang parameter through
// every signature.
func WithLang(ctx context.Context, lang Lang) context.Context {
	return context.WithValue(ctx, ctxKey{}, lang)
}

// FromContext reads the language put there by WithLang; DefaultLang when
// there is none (background work, tests).
func FromContext(ctx context.Context) Lang {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(Lang); ok {
			return l
		}
	}
	return DefaultLang
}

// Tc is T against the language carried by ctx.
func Tc(ctx context.Context, key string, args ...any) string {
	return T(FromContext(ctx), key, args...)
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

// LangMiddleware puts the request's language (see LangFromRequest) into
// the request context, so handlers and everything they call can use
// Tc/FromContext instead of re-reading the header.
func LangMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithLang(r.Context(), LangFromRequest(r))))
	})
}
