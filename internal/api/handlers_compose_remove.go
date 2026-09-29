package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/parse"
	"github.com/piqab/nkt/internal/site"
)

// Удаление compose-стека с хоста (удаление конвейера на хабе): compose
// down, затем каталог стека. Без томов каталог не стирается, а
// переносится в .nkt-removed: в нём могут лежать данные bind-mount
// (./data), и «удалить конвейер» не должно означать «потерять данные».

// KindComposeRemove — задание «убрать стек».
const KindComposeRemove = "compose.remove"

// ComposeRemovedDir — куда переносятся каталоги убранных стеков.
const ComposeRemovedDir = parse.ComposeStacksDir + "/.nkt-removed"

// ComposeRemoveParams — вход задания.
type ComposeRemoveParams struct {
	Project string `json:"project"`
	// Volumes — удалить и тома (down -v), и каталог стека целиком.
	Volumes bool `json:"volumes"`
	// Images — удалить образы стека (down --rmi all).
	Images bool `json:"images"`
}

// composeRemoveScript — каталог стека: $1 — каталог, $2 — куда перенести
// (пусто — удалить целиком).
const composeRemoveScript = `set -e
[ -d "$1" ] || exit 0
if [ -z "$2" ]; then
  rm -rf -- "$1"
else
  mkdir -p -- "$(dirname "$2")"
  mv -- "$1" "$2"
fi
`

// handleComposeRemove — POST /compose/stacks/remove {project, volumes,
// images}: задание хоста.
func (s *Server) handleComposeRemove(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, msgs.Tc(r.Context(), "api.backgroundJobsAreUnavailable"))
		return
	}
	var p ComposeRemoveParams
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !site.ValidName(p.Project) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("compose.badProject", p.Project))
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind: KindComposeRemove, TitleKey: "compose.removeTitle", TitleArgs: []any{p.Project},
		Queue: "compose:" + p.Project, Author: user, Steps: 2, Params: p,
	})
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	s.db.Audit(r.Context(), user, "compose.remove", p.Project, "ok", map[string]any{"job": id, "volumes": p.Volumes, "images": p.Images})
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

// composeRemoveRunner убирает стек.
type composeRemoveRunner struct{ s *Server }

func (d *composeRemoveRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ComposeRemoveParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	if !site.ValidName(p.Project) {
		return msgs.Errorf("compose.badProject", p.Project)
	}
	s := d.s
	c := s.scanner.Collector()
	defer s.rescanLater()
	dir := composeStackDir(p.Project)

	// 1. Контейнеры и сеть.
	jc.StepKey(1, 2, "compose.stepDown", p.Project)
	file := composeFileIn(c, dir)
	if file == "" {
		jc.Log("compose.removeNoStack", dir)
		return nil
	}
	engine := composeEngine(ctx, c)
	if engine == "" {
		return msgs.Errorf("compose.noEngine")
	}
	args := append(composeArgs(c, p.Project, file), "down", "--remove-orphans")
	if p.Volumes {
		args = append(args, "-v")
	}
	if p.Images {
		args = append(args, "--rmi", "all")
	}
	jc.Logf("$ %s %s", engine, strings.Join(args, " "))
	res, err := c.RunTimeout(ctx, 10*time.Minute, engine, args...)
	for _, l := range strings.Split(strings.TrimSpace(res.Output()), "\n") {
		if strings.TrimSpace(l) != "" {
			jc.Logf("      %s", l)
		}
	}
	if err != nil {
		return err
	}
	if !res.OK() {
		return msgs.Errorf("compose.commandFailed", "down", res.ExitCode)
	}

	// 2. Каталог стека.
	jc.StepKey(2, 2, "compose.stepDir", dir)
	dest := ""
	if !p.Volumes {
		dest = ComposeRemovedDir + "/" + p.Project + "-" + time.Now().Format("20060102-150405")
	}
	if s.cfg.Mode == config.ModeFixtures {
		// На фикстурах каталог — часть снимка, а не хоста.
		jc.Log("compose.removeDirSkipped", dir)
	} else {
		out, err := unrestrictedCommand(nil, "sh", "-c", composeRemoveScript, "sh", dir, dest).CombinedOutput()
		if err != nil {
			return msgs.Errorf("compose.removeDirFailed", dir, strings.TrimSpace(string(out))+" "+err.Error())
		}
	}
	if dest == "" {
		jc.Log("compose.removeDirDeleted", dir)
	} else {
		jc.Log("compose.removeDirMoved", dir, dest)
	}
	jc.Log("compose.removed", p.Project)
	return nil
}
