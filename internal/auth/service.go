package auth

import (
	"context"
	"errors"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/store"
)

// SessionCookie is the name of the cookie carrying the opaque session token.
const SessionCookie = "nkt_session"

// Errors returned by Login.
var (
	ErrInvalidCredentials = msgs.Errorf("auth.invalidUsernamePassword")
	ErrTooManyAttempts    = msgs.Errorf("auth.tooManyFailedLoginAttempts")
	ErrMutationsDisabled  = msgs.Errorf("auth.changesAreDisabledNKTALLOW")
)

type ctxKey int

const userKey ctxKey = iota

// Service ties accounts, sessions and HTTP middleware together.
type Service struct {
	db  *store.DB
	cfg *config.Config

	attempts *AttemptLimiter
	tokens   TokenAuthenticator
}

// TokenAuthenticator проверяет запрос с API-токеном (заголовок
// Authorization: Bearer nkt_… или подписанный X-NKT-API-*). Токены есть
// только у хаба: он и проверяет, и решает, какие вызовы токену открыты.
// Возвращает контекст с пользователем токена; отказ — *TokenError.
type TokenAuthenticator interface {
	Authenticate(r *http.Request) (context.Context, error)
}

// TokenError — отказ токену: код ответа и причина.
type TokenError struct {
	Code int
	Err  error
}

func (e *TokenError) Error() string { return e.Err.Error() }
func (e *TokenError) Unwrap() error { return e.Err }

// SetTokenAuth включает API-токены (хаб).
func (s *Service) SetTokenAuth(t TokenAuthenticator) { s.tokens = t }

// IsTokenRequest — запрос пришёл с API-токеном, а не с сессией браузера.
func IsTokenRequest(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.Header.Get("X-NKT-API-Key") != ""
}

const readGrantKey ctxKey = iota + 100

// WithReadGrant — токену с ролью «чтение» этот GET открыт, хотя маршрут
// за RequireAdmin (просмотр хоста через хаб — только администраторам
// интерфейса, а токену чтения — по списку разделов).
func WithReadGrant(ctx context.Context) context.Context {
	return context.WithValue(ctx, readGrantKey, true)
}

func readGranted(r *http.Request) bool {
	g, _ := r.Context().Value(readGrantKey).(bool)
	return g && (r.Method == http.MethodGet || r.Method == http.MethodHead)
}

// NewService builds the auth service.
func NewService(db *store.DB, cfg *config.Config) *Service {
	return &Service{db: db, cfg: cfg, attempts: NewAttemptLimiter()}
}

// Bootstrap creates the initial admin account on an empty database and returns
// the generated password when one had to be invented.
func (s *Service) Bootstrap(ctx context.Context) (string, error) {
	n, err := s.db.CountUsers(ctx)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}
	password := s.cfg.BootstrapAdminPassword
	generated := ""
	if password == "" {
		if password, err = GeneratePassword(); err != nil {
			return "", err
		}
		generated = password
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	if _, err := s.db.CreateUser(ctx, s.cfg.BootstrapAdminUser, hash, store.RoleAdmin); err != nil {
		return "", err
	}
	return generated, nil
}

// Login validates credentials and opens a session.
func (s *Service) Login(ctx context.Context, username, password, userAgent string) (string, time.Time, store.User, error) {
	if !s.attempts.Allow(username) {
		return "", time.Time{}, store.User{}, ErrTooManyAttempts
	}

	user, err := s.db.UserByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		// Spend comparable time so a missing account is not distinguishable by timing.
		_, _ = HashPassword(password)
		s.attempts.Fail(username)
		return "", time.Time{}, store.User{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", time.Time{}, store.User{}, err
	}
	if user.Disabled {
		s.attempts.Fail(username)
		return "", time.Time{}, store.User{}, ErrInvalidCredentials
	}

	ok, err := VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		s.attempts.Fail(username)
		return "", time.Time{}, store.User{}, ErrInvalidCredentials
	}
	s.attempts.Clear(username)

	token, err := NewToken()
	if err != nil {
		return "", time.Time{}, store.User{}, err
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.db.CreateSession(ctx, token, user.ID, expires, userAgent); err != nil {
		return "", time.Time{}, store.User{}, err
	}
	_ = s.db.TouchLogin(ctx, user.ID)
	return token, expires, user, nil
}

// Logout closes a session.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.db.DeleteSession(ctx, token)
}

// ChangePassword updates the caller's own password after verifying the old one.
func (s *Service) ChangePassword(ctx context.Context, username, oldPassword, newPassword string) error {
	user, err := s.db.UserByName(ctx, username)
	if err != nil {
		return err
	}
	ok, err := VerifyPassword(oldPassword, user.PasswordHash)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.db.SetPasswordHash(ctx, username, hash)
}

// --------------------------------------------------------------------- cookies

// SetSessionCookie writes the session cookie on a successful login.
func (s *Service) SetSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie removes the session cookie on logout.
func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// TokenFromRequest reads the session token out of the request cookie.
func TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// ------------------------------------------------------------------ middleware

// RequireAuth rejects unauthenticated requests and puts the user in the context.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Уже проверен: хаб передал запрос встроенному API машины хаба
		// (/hosts/local/*) с тем же контекстом.
		if _, ok := UserFromContext(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		if IsTokenRequest(r) {
			lang := msgs.LangFromRequest(r)
			if s.tokens == nil {
				writeAuthError(w, http.StatusUnauthorized, msgs.T(lang, "auth.tokensUnsupported"))
				return
			}
			ctx, err := s.tokens.Authenticate(r)
			if err != nil {
				code := http.StatusUnauthorized
				var te *TokenError
				if errors.As(err, &te) {
					code = te.Code
				}
				writeAuthError(w, code, msgs.Localize(lang, err))
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		token := TokenFromRequest(r)
		if token == "" {
			writeAuthError(w, http.StatusUnauthorized, msgs.T(msgs.LangFromRequest(r), "auth.loginRequired"))
			return
		}
		user, err := s.db.ResolveSession(r.Context(), token)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, msgs.T(msgs.LangFromRequest(r), "auth.sessionExpired"))
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), user)))
	})
}

// RequireAdmin additionally demands the admin role and a live mutation switch.
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, msgs.T(msgs.LangFromRequest(r), "auth.loginRequired"))
			return
		}
		if readGranted(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !user.IsAdmin() {
			writeAuthError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "auth.adminOnly"))
			return
		}
		if !s.cfg.AllowMutations {
			writeAuthError(w, http.StatusForbidden, ErrMutationsDisabled.Error())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WithUser stores a user in a context.
func WithUser(ctx context.Context, u store.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFromContext retrieves the authenticated user.
func UserFromContext(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey).(store.User)
	return u, ok
}

// Username returns the authenticated user's login, or "-" when absent.
func Username(ctx context.Context) string {
	if u, ok := UserFromContext(ctx); ok {
		return u.Username
	}
	return "-"
}

func writeAuthError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":` + quoteJSON(msg) + `}`))
}

func quoteJSON(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			out = append(out, '\\', r)
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, r)
		}
	}
	return string(append(out, '"'))
}
