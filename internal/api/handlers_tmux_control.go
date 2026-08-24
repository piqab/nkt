package api

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/config"
)

// tmuxControlTimeout bounds every tmux control-plane call below (listing
// windows, switching, splitting, ...) — these are local IPC to the tmux
// server's own socket, not network calls, so they normally return in
// milliseconds; this is a backstop against a wedged tmux server, not a
// realistic budget.
const tmuxControlTimeout = 5 * time.Second

// runTmux runs `tmux <args...>` against the same tmux server the terminal
// page's tmux mode attaches to (see handleTerminalWS: same tmuxSessionName,
// same privilege-drop decision via s.cfg.TerminalUser — tmux's default
// socket lives under a per-uid path, so reaching the right server means
// running as the exact same user, not just "some user with tmux on PATH").
// Returns tmux's own combined stdout+stderr, trimmed, alongside any error —
// callers use the output as the human-readable reason on failure (tmux's
// own messages, e.g. "can't find window: 7", are already clear).
func (s *Server) runTmux(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, tmuxControlTimeout)
	defer cancel()

	argv := append([]string{"tmux"}, args...)
	var cmd *exec.Cmd
	if s.cfg.TerminalUser != "" {
		var err error
		cmd, err = unrestrictedQuietCommandAsUser(ctx, nil, s.cfg.TerminalUser, argv...)
		if err != nil {
			return "", err
		}
	} else {
		cmd = unrestrictedQuietCommand(ctx, nil, argv...)
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// tmuxWindow is one row of `tmux list-windows` — see handleTmuxWindows.
type tmuxWindow struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Panes  int    `json:"panes"`
}

// handleTmuxWindows lists the windows in the terminal page's tmux session
// (tmuxSessionName) — the data behind the clickable window list next to the
// terminal, so switching/creating/closing windows doesn't require typing
// tmux's own key sequences into the shell. Running=false (session doesn't
// exist yet — nobody has opened "Открыть в tmux" on this host, or the
// session died) is not an error; it just means there is nothing to list
// yet, same shape either way so the frontend doesn't need a separate
// branch for "not started" versus "started, no windows" (which tmux itself
// never actually allows — a session always has at least one window).
func (s *Server) handleTmuxWindows(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TerminalEnabled || s.cfg.Mode == config.ModeFixtures {
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "windows": []tmuxWindow{}})
		return
	}

	out, err := s.runTmux(r.Context(), "list-windows", "-t", tmuxSessionName,
		"-F", "#{window_index}\t#{window_name}\t#{window_active}\t#{window_panes}")
	if err != nil {
		// tmux exits non-zero (and prints "can't find session: nkt") when
		// the session simply doesn't exist yet — the overwhelmingly common
		// case (nobody has opened a tmux terminal on this host yet), not a
		// real failure worth surfacing as an error to the frontend.
		writeJSON(w, http.StatusOK, map[string]any{"running": false, "windows": []tmuxWindow{}})
		return
	}

	windows := []tmuxWindow{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			continue
		}
		index, _ := strconv.Atoi(fields[0])
		panes, _ := strconv.Atoi(fields[3])
		windows = append(windows, tmuxWindow{
			Index:  index,
			Name:   fields[1],
			Active: fields[2] == "1",
			Panes:  panes,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": true, "windows": windows})
}

// tmuxActionRequest is handleTmuxAction's request body — Index is required
// by "select-window" and "kill-window", ignored by the rest (they always
// target tmux's own notion of "the current window").
type tmuxActionRequest struct {
	Action string `json:"action"`
	Index  int    `json:"index"`
}

// handleTmuxAction runs one tmux control command against the terminal
// page's tmux session — the click-to-act counterpart of the key sequences
// listed in the terminal page's own tmux cheat sheet (Ctrl+b n/c/x/%/"),
// for the subset that makes sense as a discrete, targetable button: switch/
// create/close a window, split the current one. Deliberately not every
// tmux command has a button here — pane-level navigation and scroll/copy
// mode stay keyboard-only (see the frontend's static reference for those).
func (s *Server) handleTmuxAction(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.TerminalEnabled || s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, "терминал выключен на этом хосте")
		return
	}

	var req tmuxActionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var args []string
	switch req.Action {
	case "select-window":
		args = []string{"select-window", "-t", fmt.Sprintf("%s:%d", tmuxSessionName, req.Index)}
	case "new-window":
		args = []string{"new-window", "-t", tmuxSessionName}
	case "kill-window":
		args = []string{"kill-window", "-t", fmt.Sprintf("%s:%d", tmuxSessionName, req.Index)}
	case "split-h":
		args = []string{"split-window", "-h", "-t", tmuxSessionName}
	case "split-v":
		args = []string{"split-window", "-v", "-t", tmuxSessionName}
	default:
		writeError(w, http.StatusBadRequest, "неизвестное действие: "+req.Action)
		return
	}

	if out, err := s.runTmux(r.Context(), args...); err != nil {
		reason := out
		if reason == "" {
			reason = err.Error()
		}
		writeError(w, http.StatusBadRequest, "команда tmux не выполнена: "+reason)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
