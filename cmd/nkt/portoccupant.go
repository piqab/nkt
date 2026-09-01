package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/parse"
)

// diagnoseListenError enriches a failed ListenAndServe(TLS) error with
// whatever process is already holding addr's port, when the failure is
// EADDRINUSE. Go's own message — "listen tcp 127.0.0.1:8077: bind: address
// already in use" — names the address but never what's actually squatting
// on it, leaving "kill it and go figure out what it was yourself" as the
// only next step. Best-effort and silent on any failure of its own (`ss`
// missing, not running as root, nothing found): a diagnostic that can't
// identify the culprit must never mask or replace the original bind error.
func diagnoseListenError(ctx context.Context, err error, addr string) error {
	if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
		return err
	}
	_, portStr, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		return err
	}
	port, convErr := strconv.Atoi(portStr)
	if convErr != nil {
		return err
	}

	who := portOccupant(ctx, port)
	if who == "" {
		return err
	}
	return fmt.Errorf("%w (port already held by: %s)", err, who)
}

// portOccupant asks `ss` who is already listening on port — the real host's
// state regardless of NKT_MODE, since a port conflict is an OS-level fact,
// not something fixtures mode should ever simulate. Reuses the exact same
// `ss -tulpnH` parsing the declared-not-listening/listening-not-declared
// analyze rules already depend on, via a throwaway collect.Local rather
// than whatever collector this process's own NKT_MODE would otherwise pick,
// so this still works (and reports the truth) even when nkt itself is
// running in fixtures mode. Returns "" — never an error — on anything short
// of a clean answer.
func portOccupant(ctx context.Context, port int) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	c := collect.NewLocal("", "", 5*time.Second)
	listeners, status := parse.Listeners(ctx, c)
	if !status.Available {
		return ""
	}

	seen := map[string]bool{}
	var found []string
	for _, l := range listeners {
		if l.Port != port || l.Process == "" {
			continue
		}
		desc := l.Process
		if l.PID != 0 {
			desc = fmt.Sprintf("%s (pid %d)", l.Process, l.PID)
		}
		// Dual-stack (tcp/tcp6) or multiple bound addresses for the same
		// port commonly produce more than one `ss` row for the same
		// process — report it once.
		if !seen[desc] {
			seen[desc] = true
			found = append(found, desc)
		}
	}
	return strings.Join(found, ", ")
}
