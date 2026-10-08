package api

import (
	"context"
	"encoding/json"
	"net/http"
	gopath "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
)

// Операции хоста, которые бывают долгими, но выполняются кодом nkt, а не
// одной командой (распаковка архива, генерация локалей, удаление
// пользователя с домашним каталогом), — с ?job=1 идут заданием: та же
// функция, что вызывает обработчик, только в задании с журналом. Запрос
// браузера обрывается через 30 секунд и раньше прерывал их на полпути;
// задание видно в индикаторе фоновых операций.

// KindHostOp — вид задания «операция хоста».
const KindHostOp = "host.op"

// HostOpParams — вход задания: имя операции и её параметры.
type HostOpParams struct {
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
}

type extractArgs struct {
	Path string `json:"path"`
	Dest string `json:"dest"`
}

type localesArgs struct {
	Generate []string `json:"generate,omitempty"`
	Default  string   `json:"default,omitempty"`
}

// archiveLoadArgs — docker (podman) load архива из каталога архивов.
type archiveLoadArgs struct {
	Engine string `json:"engine"`
	Path   string `json:"path"`
	Remove bool   `json:"remove,omitempty"`
}

type osUserDeleteArgs struct {
	Name string `json:"name"`
	Home bool   `json:"home"`
}

// longCallTimeout — предел долгих вызовов, которые хаб делает на хосте
// изнутри своих заданий и ждёт с данными в ответе (проверка compose-стека,
// скан образов, удаление сайта, сеть WireGuard).
const longCallTimeout = 30 * time.Minute

// longCall снимает с такого вызова общие сроки: 4-минутный предел
// маршрутов (он отменял контекст, и команда обрывалась) и 2-минутный срок
// записи ответа http.Server (ответ после него уже не уходил). Контекст —
// свой, на longCallTimeout, без отмены по пределу маршрута.
func longCall(w http.ResponseWriter, r *http.Request) (context.Context, context.CancelFunc) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(longCallTimeout + time.Minute))
	return context.WithTimeout(context.WithoutCancel(r.Context()), longCallTimeout)
}

// startHostOp заводит задание операции op с параметрами args.
func (s *Server) startHostOp(w http.ResponseWriter, r *http.Request, op string, args any, titleKey string, titleArgs []any, queue string) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	raw, err := json.Marshal(args)
	if err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindHostOp, TitleKey: titleKey, TitleArgs: titleArgs, Queue: queue,
		Author: auth.Username(r.Context()), Steps: 1, Params: HostOpParams{Op: op, Args: raw},
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// hostOpRunner выполняет KindHostOp.
type hostOpRunner struct{ s *Server }

// Resumable — нет: операцию с середины не продолжить.
func (h *hostOpRunner) Resumable() bool { return false }

func (h *hostOpRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p HostOpParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	s, user := h.s, jc.Job.Author
	switch p.Op {
	case "files.extract":
		var a extractArgs
		if err := json.Unmarshal(p.Args, &a); err != nil {
			return err
		}
		if s.files == nil {
			return msgs.Errorf("api.fileBrowserUnavailable")
		}
		dest := a.Dest
		if dest == "" {
			dest = gopath.Dir(a.Path)
		}
		jc.StepKey(1, 1, "hostop.extract", a.Path)
		jc.Log("hostop.extractTo", a.Path, dest)
		err := s.files.Extract(ctx, a.Path, dest)
		s.db.Audit(ctx, user, "files.extract", a.Path, auditResult(err), errText(err))
		if err == nil {
			jc.Log("hostop.done")
		}
		return err
	case "locales":
		var a localesArgs
		if err := json.Unmarshal(p.Args, &a); err != nil {
			return err
		}
		jc.StepKey(1, 1, "hostop.locales")
		if len(a.Generate) > 0 {
			jc.Log("hostop.localesGenerate", strings.Join(a.Generate, " "))
			err := s.sysconfig.GenerateLocales(ctx, a.Generate)
			s.db.Audit(ctx, user, "system.locale.generate", strings.Join(a.Generate, " "), auditResult(err), errText(err))
			if err != nil {
				return err
			}
		}
		if a.Default != "" {
			jc.Log("hostop.localesDefault", a.Default)
			err := s.sysconfig.SetLocale(ctx, a.Default)
			s.db.Audit(ctx, user, "system.locale", a.Default, auditResult(err), errText(err))
			if err != nil {
				return err
			}
		}
		jc.Log("hostop.done")
		return nil
	case "archive.load":
		var a archiveLoadArgs
		if err := json.Unmarshal(p.Args, &a); err != nil {
			return err
		}
		// Только движки и только файл из каталога архивов: параметры
		// задания хранятся в базе и не должны вести к чему-то ещё.
		if (a.Engine != "docker" && a.Engine != "podman") || filepath.Dir(a.Path) != filepath.Clean(s.archiveDir()) {
			return msgs.Errorf("hostop.unknown", p.Op)
		}
		engineName := map[string]string{"docker": "Docker", "podman": "Podman"}[a.Engine]
		jc.StepKey(1, 1, "archives.stepLoad", engineName)
		argv := []string{a.Engine, "load", "-i", a.Path}
		jc.Logf("$ %s", strings.Join(argv, " "))
		code, err := RunToolingStream(ctx, func(format string, args ...any) { jc.Logf(format, args...) }, argv...)
		if err == nil && code != 0 {
			err = msgs.Errorf("cmdjob.exitCode", code)
		}
		if err != nil {
			return err
		}
		jc.Log("archives.loaded", engineName)
		if a.Remove {
			removeLoadedArchive(jc, a.Path)
		}
		s.rescanLater()
		return nil
	case "osuser.delete":
		var a osUserDeleteArgs
		if err := json.Unmarshal(p.Args, &a); err != nil {
			return err
		}
		jc.StepKey(1, 1, "hostop.osUserDelete", a.Name)
		err := s.osusers.Delete(ctx, a.Name, a.Home)
		target := a.Name
		if a.Home {
			target += " +home"
		}
		s.db.Audit(ctx, user, "osuser.delete", target, auditResult(err), errText(err))
		if err == nil {
			jc.Log("hostop.done")
		}
		return err
	}
	return msgs.Errorf("hostop.unknown", p.Op)
}
