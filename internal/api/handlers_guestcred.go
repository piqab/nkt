package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/cmdjob"
	"github.com/piqab/nkt/internal/guestcred"
	"github.com/piqab/nkt/internal/msgs"
)

// Логин и пароль гостей (инстансов LXD и машин libvirt), заданные через
// nkt: сведения без пароля — всем, пароль — администратору по кнопке с
// записью в аудит, смена — фоновым заданием.

func (s *Server) guestTarget(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	kind, name := chi.URLParam(r, "kind"), chi.URLParam(r, "name")
	if s.guestCreds == nil || !guestcred.ValidTarget(kind, name) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "guestcred.invalid"))
		return "", "", false
	}
	return kind, name, true
}

// handleGuestCredInfo — GET /guests/{kind}/{name}/credentials.
func (s *Server) handleGuestCredInfo(w http.ResponseWriter, r *http.Request) {
	kind, name, ok := s.guestTarget(w, r)
	if !ok {
		return
	}
	info, err := s.guestCreds.Info(r.Context(), kind, name)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// handleGuestCredReveal — POST /guests/{kind}/{name}/credentials/reveal.
func (s *Server) handleGuestCredReveal(w http.ResponseWriter, r *http.Request) {
	kind, name, ok := s.guestTarget(w, r)
	if !ok {
		return
	}
	user, pass, err := s.guestCreds.Reveal(r.Context(), kind, name)
	if err != nil {
		writeErr(w, r, http.StatusNotFound, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "guest.password.reveal", kind+":"+name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"user": user, "password": pass})
}

type guestPasswordRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
	Generate bool   `json:"generate"`
}

// guestPasswordCommand — команда задания, ставящая пароль внутри гостя.
// Пароль в неё не вписывается: он подставляется из хранилища во
// временный сценарий по ссылке.
func guestPasswordCommand(kind, name, user string) cmdjob.Command {
	ref := guestcred.Ref(kind, name)
	var script string
	if kind == "lxd" {
		lxc := guestcred.ShellQuote(hostTool("lxc"))
		n := guestcred.ShellQuote(name)
		// VM LXD отвечает на lxc exec, только когда поднялся lxd-agent —
		// после создания это до пары минут.
		script = fmt.Sprintf("for i in $(seq 1 90); do %s exec %s -- true >/dev/null 2>&1 && break; sleep 2; done\n", lxc, n)
		if user != "root" {
			script += fmt.Sprintf("%s exec %s -- sh -c %s\n", lxc, n, guestcred.ShellQuote(
				"id -u "+user+" >/dev/null 2>&1 || useradd -m -s /bin/bash "+user+" 2>/dev/null || adduser -D -s /bin/sh "+user+"; "+
					"usermod -aG sudo "+user+" 2>/dev/null || usermod -aG wheel "+user+" 2>/dev/null || addgroup "+user+" wheel 2>/dev/null || true"))
		}
		script += fmt.Sprintf("printf '%%s:%%s\\n' %s {secret} | %s exec %s -- chpasswd\n", user, lxc, n)
	} else {
		script = fmt.Sprintf("%s -c qemu:///system set-user-password %s %s {secret}\n",
			guestcred.ShellQuote(hostTool("virsh")), guestcred.ShellQuote(name), user)
	}
	script += "echo " + guestcred.ShellQuote("password set for "+user) + "\n"
	return cmdjob.Command{Script: script, SecretRef: ref, StepKey: "guest.stepPassword", StepArgs: []any{user, name}}
}

// handleGuestPassword — POST /guests/{kind}/{name}/password {user,
// password|generate}: сохранить и поставить внутри гостя заданием.
func (s *Server) handleGuestPassword(w http.ResponseWriter, r *http.Request) {
	kind, name, ok := s.guestTarget(w, r)
	if !ok {
		return
	}
	var req guestPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	req.User = strings.TrimSpace(req.User)
	if req.Generate {
		req.Password = guestcred.Generate()
	}
	author := auth.Username(r.Context())
	if err := s.guestCreds.Put(r.Context(), kind, name, req.User, req.Password, author); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	s.startCmdJob(w, r, "guest.jobPassword", []any{req.User, name}, "guest:"+kind+":"+name,
		cmdjob.Params{Commands: []cmdjob.Command{guestPasswordCommand(kind, name, req.User)}}, "guest.password.set", kind+":"+name)
}
