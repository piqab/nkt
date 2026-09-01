package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/piqab/nkt/internal/config"
)

// buildSelfUpdateRequest assembles a valid multipart/form-data body for
// handleSelfUpdate, overridable per-field by the caller so tests can corrupt
// exactly one piece at a time.
func buildSelfUpdateRequest(t *testing.T, binary []byte, unit, env, sha string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if binary != nil {
		part, err := w.CreateFormFile("binary", "nkt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(binary); err != nil {
			t.Fatal(err)
		}
	}
	for field, value := range map[string]string{"unit": unit, "env": env, "sha256": sha} {
		if value == "" {
			continue
		}
		if err := w.WriteField(field, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/self-update", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// TestHandleSelfUpdateGates locks in every guard/validation branch that
// returns before ever touching the filesystem or spawning
// systemd-run/bash — the success path deliberately has no test here, since
// it really would install a binary and restart a systemd unit named
// "netknownsthat" on whatever machine runs the test.
func TestHandleSelfUpdateGates(t *testing.T) {
	t.Run("refused outside ModeLocal", func(t *testing.T) {
		for _, mode := range []config.Mode{config.ModeFixtures, config.ModeHub} {
			cfg := &config.Config{Mode: mode}
			s := &Server{cfg: cfg}

			req := buildSelfUpdateRequest(t, []byte("x"), "unit", "env", "deadbeef")
			rec := httptest.NewRecorder()
			s.handleSelfUpdate(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("mode %s: status = %d, want %d (body: %s)", mode, rec.Code, http.StatusForbidden, rec.Body.String())
			}
		}
	})

	t.Run("missing binary file", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeLocal, DataDir: t.TempDir()}
		s := &Server{cfg: cfg}

		req := buildSelfUpdateRequest(t, nil, "unit", "env", "deadbeef")
		rec := httptest.NewRecorder()
		s.handleSelfUpdate(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("missing unit/env/sha256", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeLocal, DataDir: t.TempDir()}
		s := &Server{cfg: cfg}

		req := buildSelfUpdateRequest(t, []byte("x"), "", "", "")
		rec := httptest.NewRecorder()
		s.handleSelfUpdate(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		cfg := &config.Config{Mode: config.ModeLocal, DataDir: t.TempDir()}
		s := &Server{cfg: cfg}

		req := buildSelfUpdateRequest(t, []byte("real binary bytes"), "unit content", "env content", "0000000000000000000000000000000000000000000000000000000000000000")
		rec := httptest.NewRecorder()
		s.handleSelfUpdate(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})
}

// TestSystemdRunBackgroundArgs is a pure check of the flags, mirroring
// TestSystemdRunArgs in pty_sandbox_test.go — the crucial difference this
// locks in is the *absence* of --pty: a fire-and-forget command must not
// wait on a controlling terminal nothing here provides.
func TestSystemdRunBackgroundArgs(t *testing.T) {
	args := systemdRunBackgroundArgs("bash", "-c", "true")

	for _, unwanted := range []string{"--pty"} {
		for _, a := range args {
			if a == unwanted {
				t.Errorf("systemdRunBackgroundArgs() must not include %q: %v", unwanted, args)
			}
		}
	}
	for _, want := range []string{"--collect", "--quiet", "-p", "ProtectSystem=no", "CapabilityBoundingSet=~"} {
		found := false
		for _, a := range args {
			if a == want {
				found = true
			}
		}
		if !found {
			t.Errorf("systemdRunBackgroundArgs() missing %q in %v", want, args)
		}
	}
	if len(args) < 3 || args[len(args)-3] != "bash" || args[len(args)-2] != "-c" || args[len(args)-1] != "true" {
		t.Errorf("systemdRunBackgroundArgs() target command not at the end: %v", args)
	}
}

// TestUnrestrictedBackgroundCommandFallsBackOutsideSystemd mirrors
// TestUnrestrictedCommandFallsBackOutsideSystemd: no INVOCATION_ID means a
// plain exec.Command, same as unrestrictedCommand's own fallback.
func TestUnrestrictedBackgroundCommandFallsBackOutsideSystemd(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")

	cmd := unrestrictedBackgroundCommand("echo", "hi")

	if got := cmd.Args; len(got) != 2 || got[0] != "echo" || got[1] != "hi" {
		t.Errorf("cmd.Args = %v, want [echo hi]", got)
	}
}
