package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/store"
)

// TestHandleUserPatchNeverAuditsThePlaintextPassword guards against a real
// leak: handleUserPatch used to audit the raw request struct, which
// includes the new plaintext password whenever one was set — readable by
// any authenticated user via GET /audit, including a viewer, since that
// route only requires RequireAuth, not RequireAdmin. Resetting someone
// else's password is an ordinary admin action (the "I forgot my password"
// recovery path), so this fired on entirely routine use, not just an
// attack.
func TestHandleUserPatchNeverAuditsThePlaintextPassword(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.CreateUser(context.Background(), "victim", "old-hash", store.RoleViewer); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	s := &Server{db: db}

	const secret = "S3cr3tNewPassw0rd!"
	body := `{"password":"` + secret + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/users/victim", strings.NewReader(body))
	req = withChiParams(req, map[string]string{"name": "victim"})
	req = req.WithContext(auth.WithUser(req.Context(), store.User{Username: "admin", Role: store.RoleAdmin}))

	rec := httptest.NewRecorder()
	s.handleUserPatch(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	entries, err := db.ListAudit(context.Background(), store.AuditFilter{Action: "user.update", Limit: 10})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d user.update audit entries, want 1: %+v", len(entries), entries)
	}
	if strings.Contains(entries[0].Detail, secret) {
		t.Fatalf("audit detail contains the plaintext password: %q", entries[0].Detail)
	}
	if !strings.Contains(entries[0].Detail, "password_changed") {
		t.Errorf("audit detail should still record that a password change happened: %q", entries[0].Detail)
	}
}
