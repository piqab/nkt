package api

import (
	"context"
	"github.com/piqab/nkt/internal/msgs"
	"net/http"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
)

// Запасной путь для случая, когда каталог не открыть ни изнутри юнита
// (ProtectSystem=strict), ни снаружи (выход из песочницы недоступен: D-Bus
// молчит, а setns запрещён). Тогда единственное лекарство — расширить сам
// юнит и перезапустить службу, и раньше это оператор делал руками по
// подсказке в тексте ошибки.
//
// Перезапуск здесь не косметика: пока служба не перечитает юнит, каталог
// так и останется смонтированным только для чтения — сам drop-in ничего не
// меняет до daemon-reload.

// allowWriteRequest — путь файла, который оператор пытается править.
type allowWriteRequest struct {
	Path string `json:"path"`
}

// allowWriteResponse честно разделяет два исхода: служба перезапускается
// сама или оператору остаётся выполнить команды вручную. Второе — не
// ошибка: drop-in уже готов, не хватает только systemd, до которого из
// этого состояния может не быть связи.
type allowWriteResponse struct {
	Status     string   `json:"status"`
	DropIn     string   `json:"drop_in"`
	Changed    bool     `json:"changed"`
	Restarting bool     `json:"restarting"`
	Commands   []string `json:"commands,omitempty"`
	Message    string   `json:"message,omitempty"`
}

// manualReloadCommands — то, что придётся выполнить самому, если systemd
// отсюда недостижим.
var manualReloadCommands = []string{
	"sudo systemctl daemon-reload",
	"sudo systemctl restart netknownsthat",
}

func (s *Server) handleConfigAllowWrite(w http.ResponseWriter, r *http.Request) {
	var req allowWriteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	user := auth.Username(r.Context())

	dropIn, changed, err := s.configs.AllowWrite(req.Path)
	if err != nil {
		s.db.Audit(r.Context(), user, "config.allowWrite", req.Path, "error", err.Error())
		fail(w, r, err)
		return
	}
	if !changed {
		// Каталог уже в списке: либо перезапуск ещё не делали, либо юнит
		// перечитан, а мешает что-то другое. Перезапускать службу «на
		// всякий случай» нельзя — это рвёт открытые терминалы и журналы.
		writeJSON(w, http.StatusOK, allowWriteResponse{
			Status: "already", DropIn: dropIn, Commands: manualReloadCommands,
			Message: msgs.Tc(r.Context(), "api.dirAlreadyOpen", control.WritablePathsDropIn),
		})
		return
	}
	s.db.Audit(r.Context(), user, "config.allowWrite", req.Path, "ok", dropIn)

	// daemon-reload синхронно: только он говорит, дошли ли мы до systemd
	// вообще. Перезапуск после него — уже в фоне, иначе ответ не успеет
	// уйти: systemctl restart убивает этот самый процесс.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	res, runErr := RunUnrestricted(ctx, "systemctl", "daemon-reload")
	if runErr != nil || res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if runErr != nil {
			detail = runErr.Error()
		}
		writeJSON(w, http.StatusOK, allowWriteResponse{
			Status: "manual", DropIn: dropIn, Changed: true, Commands: manualReloadCommands,
			Message: msgs.Tc(r.Context(), "api.dirOpenedReloadFailed", dropIn, detail),
		})
		return
	}

	cmd := UnrestrictedBackgroundCommand("sh", "-c", "sleep 1; systemctl restart netknownsthat")
	if err := cmd.Start(); err != nil {
		writeJSON(w, http.StatusOK, allowWriteResponse{
			Status: "manual", DropIn: dropIn, Changed: true, Commands: manualReloadCommands[1:],
			Message: msgs.Tc(r.Context(), "api.dirOpenedRestartLeft", err),
		})
		return
	}
	// Не Wait(): перезапуск убьёт этот процесс раньше, чем команда
	// завершится, — тот же расчёт, что и у самообновления.
	writeJSON(w, http.StatusOK, allowWriteResponse{
		Status: "ok", DropIn: dropIn, Changed: true, Restarting: true,
	})
}
