package hub

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/profile"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Выполнение сценария — заданием хаба: шаг за строкой, журнал, остановка
// на первой ошибке, продолжение после перезапуска с той же строки.
//
// Каждый шаг — те же вызовы, что делают кнопки: хосты заводятся через
// Manager, nkt ставится StartInstall, а всё, что происходит на хосте,
// уходит ему по API — большей частью как маленький профиль (пакеты,
// службы, firewall, файлы, стек compose): у профиля уже есть план,
// проверка и своё задание с журналом на хосте. Пароли из «password ask»
// живут в памяти до конца задания и в базу не пишутся.

// KindScriptRun — вид задания.
const KindScriptRun = "script.run"

// ScriptRunParams — вход задания. Текст фиксируется на момент запуска:
// правка сценария во время выполнения его не меняет.
type ScriptRunParams struct {
	ScriptID int64  `json:"script_id"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Ticket   string `json:"ticket,omitempty"`
}

type scriptRunResume struct {
	// Done — сколько шагов выполнено.
	Done int `json:"done"`
	// HostIDs — хосты, заведённые сценарием: имя → идентификатор. После
	// перезапуска по ним же продолжаем, не заводя вторых.
	HostIDs map[string]int64 `json:"host_ids,omitempty"`
}

// ScriptRunner выполняет сценарии.
type ScriptRunner struct {
	m    *Manager
	s    *Server
	mu   sync.Mutex
	pass map[string]map[string]string // билет → хост → пароль
}

// NewScriptRunner строит исполнителя.
func NewScriptRunner(s *Server) *ScriptRunner {
	return &ScriptRunner{m: s.hub, s: s, pass: map[string]map[string]string{}}
}

// Resumable — да: сделанные шаги записаны, продолжаем со следующего.
func (r *ScriptRunner) Resumable() bool { return true }

// keep прячет пароли в памяти по билету.
func (r *ScriptRunner) keep(passwords map[string]string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ticket := strconv.FormatInt(time.Now().UnixNano(), 36)
	r.pass[ticket] = passwords
	return ticket
}

func (r *ScriptRunner) password(ticket, host string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.pass[ticket][host]
	return p, ok
}

func (r *ScriptRunner) forget(ticket string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pass, ticket)
}

// hostJobTimeout — сколько ждать одного шага на хосте.
const scriptStepTimeout = 30 * time.Minute

// Run выполняет сценарий.
func (r *ScriptRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ScriptRunParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	defer r.forget(p.Ticket)
	sc, issues := script.Parse(p.Content)
	if len(issues) > 0 {
		return msgs.Errorf("hub.scriptParseFailed", issues[0].Line, issues[0].Err)
	}
	var done scriptRunResume
	if err := jc.LoadResume(&done); err != nil {
		return msgs.Errorf("hub.parsingResumeState", err)
	}
	if done.HostIDs == nil {
		done.HostIDs = map[string]int64{}
	}
	if done.Done > 0 {
		jc.Log("hub.scriptResuming", done.Done+1, len(sc.Steps))
	}
	for i := done.Done; i < len(sc.Steps); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		st := sc.Steps[i]
		jc.Step(i+1, len(sc.Steps), st.Text)
		jc.Logf("[%d/%d] %s", i+1, len(sc.Steps), st.Text)
		if err := r.step(ctx, jc, &p, &done, st); err != nil {
			return msgs.Errorf("hub.scriptStepFailed", st.Line, err)
		}
		done.Done = i + 1
		jc.SaveResume(done)
	}
	jc.Log("hub.scriptDone", len(sc.Steps))
	return nil
}

// hostByName — хост по имени: заведённый сценарием или уже известный хабу.
func (r *ScriptRunner) hostByName(ctx context.Context, done *scriptRunResume, name string) (store.Host, error) {
	if id, ok := done.HostIDs[name]; ok {
		return r.m.db.HostByID(ctx, id)
	}
	hosts, err := r.m.db.ListHosts(ctx)
	if err != nil {
		return store.Host{}, err
	}
	for _, h := range hosts {
		if h.Name == name {
			return h, nil
		}
	}
	return store.Host{}, msgs.Errorf("script.unknownHost", name)
}

func (r *ScriptRunner) step(ctx context.Context, jc *jobs.Context, p *ScriptRunParams, done *scriptRunResume, st script.Step) error {
	switch st.Kind {
	case script.KindGroup:
		var profileID int64
		if name := st.Args["profile"]; name != "" {
			id, err := r.profileID(ctx, name)
			if err != nil {
				return err
			}
			profileID = id
		}
		return r.m.CreateHostGroup(ctx, st.Name, profileID)

	case script.KindHost:
		return r.addHost(ctx, jc, p, done, st)

	case script.KindInstall:
		for _, name := range st.Hosts {
			h, err := r.hostByName(ctx, done, name)
			if err != nil {
				return err
			}
			if h.Status == store.HostStatusOnline {
				jc.Log("hub.scriptAlreadyInstalled", h.Name)
				continue
			}
			if _, err := r.m.StartInstall(ctx, h.ID, false, nil); err != nil {
				return err
			}
			jc.Log("hub.scriptInstalling", h.Name)
			if err := r.waitOnline(ctx, jc, h.ID, 15*time.Minute); err != nil {
				return err
			}
		}
		return nil

	case script.KindWait:
		h, err := r.hostByName(ctx, done, st.Host)
		if err != nil {
			return err
		}
		d, _ := time.ParseDuration(st.Args["timeout"])
		return r.waitOnline(ctx, jc, h.ID, d)
	}

	// Дальше — действия на хосте.
	h, err := r.hostByName(ctx, done, st.Host)
	if err != nil {
		return err
	}
	switch st.Kind {
	case script.KindPackages:
		if st.Action == "remove" {
			var out struct {
				Output string `json:"output"`
			}
			if _, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/system/apt/remove",
				map[string]any{"packages": st.List}, &out); err != nil {
				return err
			}
			r.logTail(jc, out.Output)
			return nil
		}
		return r.applyMini(ctx, jc, h, p.Name, map[string]any{"packages": st.List})

	case script.KindService:
		switch st.Action {
		case "start", "stop", "enable", "disable":
			svc := map[string]any{}
			if st.Action == "start" || st.Action == "stop" {
				svc["active"] = st.Action == "start"
			} else {
				svc["enabled"] = st.Action == "enable"
			}
			return r.applyMini(ctx, jc, h, p.Name, map[string]any{"services": map[string]any{st.Name: svc}})
		default: // restart, reload
			var out map[string]any
			_, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/services/"+st.Name+"/"+st.Action, map[string]any{}, &out)
			return err
		}

	case script.KindFirewall:
		port, _ := strconv.Atoi(st.Args["port"])
		if st.Action == "allow" {
			rule := map[string]any{"port": port, "proto": st.Args["proto"]}
			if from := st.Args["from"]; from != "" {
				rule["from"] = from
			}
			return r.applyMini(ctx, jc, h, p.Name, map[string]any{"firewall": map[string]any{"allow": []any{rule}}})
		}
		var out map[string]any
		_, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/firewall/rules", map[string]any{
			"action": "deny", "port": port, "protocol": st.Args["proto"], "from": st.Args["from"], "comment": "nkt script " + p.Name,
		}, &out)
		return err

	case script.KindDockerInst:
		return r.applyChanges(ctx, jc, h, p.Name, []profile.Change{{Action: profile.ActionInstallDocker, Target: "docker"}})

	case script.KindDockerStack:
		path := st.Args["path"]
		name := stackName(path)
		if st.Action == "down" {
			return r.applyChanges(ctx, jc, h, p.Name, []profile.Change{{Action: profile.ActionComposeDown, Target: path}})
		}
		if st.Block == "" {
			// Без блока поднимается файл, который уже лежит на хосте.
			return r.applyChanges(ctx, jc, h, p.Name, []profile.Change{{Action: profile.ActionComposeUp, Target: path}})
		}
		stack := map[string]any{"name": name, "path": path, "up": true, "content": st.Block}
		return r.applyMini(ctx, jc, h, p.Name, map[string]any{"compose": []any{stack}})

	case script.KindVMCreate:
		return r.createVM(ctx, jc, p, done, h, st)

	case script.KindVMAction:
		var out map[string]any
		_, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/vms/"+st.Name+"/"+st.Action, map[string]any{}, &out)
		return err

	case script.KindApplyProfile:
		prof, err := r.profileByName(ctx, st.Name)
		if err != nil {
			return err
		}
		return r.applyContent(ctx, jc, h, prof.Name, prof.Content)

	case script.KindFilePut:
		return r.applyMini(ctx, jc, h, p.Name, map[string]any{"files": []any{
			map[string]any{"path": st.Args["path"], "mode": st.Args["mode"], "content": st.Block},
		}})
	}
	return msgs.Errorf("script.unknownCommand", string(st.Kind))
}

// addHost заводит хост: с паролем из текста, с паролем, спрошенным при
// запуске, или с ключом хаба (тогда в журнал уходит строка для
// authorized_keys — без неё install не пройдёт).
func (r *ScriptRunner) addHost(ctx context.Context, jc *jobs.Context, p *ScriptRunParams, done *scriptRunResume, st script.Step) error {
	if _, ok := done.HostIDs[st.Name]; ok {
		jc.Log("hub.scriptHostAlready", st.Name)
		return nil
	}
	port, _ := strconv.Atoi(st.Args["port"])
	var id int64
	var err error
	switch st.Args["auth"] {
	case "password":
		id, err = r.m.AddHost(ctx, st.Name, st.Args["addr"], port, st.Args["user"], store.HostAuthPassword, st.Args["password"], false)
	case "password-ask":
		pw, ok := r.password(p.Ticket, st.Name)
		if !ok {
			return msgs.Errorf("hub.scriptPasswordLost", st.Name)
		}
		id, err = r.m.AddHost(ctx, st.Name, st.Args["addr"], port, st.Args["user"], store.HostAuthPassword, pw, false)
	default:
		var line string
		id, line, err = r.m.AddHostGenerated(ctx, st.Name, st.Args["addr"], port, st.Args["user"], false)
		if err == nil {
			jc.Log("hub.scriptHostKey", st.Name, line)
		}
	}
	if err != nil {
		return err
	}
	done.HostIDs[st.Name] = id
	jc.SaveResume(*done)
	if g := st.Args["group"]; g != "" {
		if err := r.m.SetHostGroup(ctx, id, g); err != nil {
			return err
		}
	}
	jc.Log("hub.scriptHostAdded", st.Name, st.Args["addr"])
	return nil
}

// waitOnline ждёт, пока хост станет «в сети» — по статусу установки и
// по опросу хаба.
func (r *ScriptRunner) waitOnline(ctx context.Context, jc *jobs.Context, hostID int64, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		h, err := r.m.db.HostByID(ctx, hostID)
		if err != nil {
			return err
		}
		if h.Status == store.HostStatusOnline {
			if ov, ok := r.m.Overview(hostID); !ok || ov.Reachable {
				// Свежий опрос подтверждает, что хост отвечает; без
				// опроса верим статусу установки.
				if _, err := r.m.HostAPI(ctx, hostID, "GET", "/api/health", nil, nil); err == nil {
					jc.Log("hub.scriptHostOnline", h.Name)
					return nil
				}
			}
		}
		if h.Status == store.HostStatusError {
			return msgs.Errorf("hub.scriptHostError", h.Name, h.ErrorMsg)
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.scriptWaitTimeout", h.Name, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// applyMini собирает мини-профиль из одного раздела и применяет его на
// хосте через план: так шаг получает проверку и журнал профиля.
func (r *ScriptRunner) applyMini(ctx context.Context, jc *jobs.Context, h store.Host, name string, section map[string]any) error {
	doc := map[string]any{"version": 1, "name": "script:" + name}
	for k, v := range section {
		doc[k] = v
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return r.applyContent(ctx, jc, h, "script:"+name, string(raw))
}

// applyContent — план и применение профиля на хосте с ожиданием его
// задания (см. GroupApplyRunner).
func (r *ScriptRunner) applyContent(ctx context.Context, jc *jobs.Context, h store.Host, name, content string) error {
	var plan profile.Plan
	if _, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/profiles/plan", map[string]string{"content": content}, &plan); err != nil {
		return msgs.Errorf("hub.buildingPlan", err)
	}
	for _, u := range plan.Unknown {
		jc.Log("hub.couldJudge", u)
	}
	if len(plan.Changes) == 0 {
		jc.Log("hub.drift")
		return nil
	}
	return r.applyChanges(ctx, jc, h, name, plan.Changes)
}

func (r *ScriptRunner) applyChanges(ctx context.Context, jc *jobs.Context, h store.Host, name string, changes []profile.Change) error {
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/profiles/apply",
		map[string]any{"name": name, "changes": changes}, &started); err != nil {
		return msgs.Errorf("hub.startingApply", err)
	}
	g := &GroupApplyRunner{m: r.m}
	return g.waitHostJob(ctx, jc, h, started.JobID)
}

// createVM создаёт машину через задание VMProvision и ждёт его; новая
// машина становится хостом сценария под своим именем.
func (r *ScriptRunner) createVM(ctx context.Context, jc *jobs.Context, p *ScriptRunParams, done *scriptRunResume, h store.Host, st script.Step) error {
	if r.s.jobs == nil {
		return msgs.Errorf("api.backgroundJobsAreUnavailable")
	}
	atoi := func(k string, def int) int {
		if v, ok := st.Args[k]; ok {
			n, _ := strconv.Atoi(v)
			return n
		}
		return def
	}
	spec := vmcreate.Spec{
		Name: st.Name, ImageID: st.Args["image"], DiskGB: atoi("disk", 20), MemoryMB: atoi("mem", 2048), VCPUs: atoi("cpu", 2),
		Network: st.Args["network"], User: st.Args["user"],
	}
	if spec.User == "" {
		spec.User = "deploy"
	}
	var profileID int64
	if name := st.Args["profile"]; name != "" {
		id, err := r.profileID(ctx, name)
		if err != nil {
			return err
		}
		profileID = id
	}
	install := st.Args["install"] == "true" || profileID != 0
	steps := 4
	if install {
		steps = 5
	}
	if profileID != 0 {
		steps = 6
	}
	jobID, err := r.s.jobs.Start(ctx, jobs.Spec{
		Kind:  KindVMProvision,
		Title: msgs.Tc(ctx, "hub.machineOnHostJobTitle", spec.Name, h.Name),
		Queue: fmt.Sprintf("vm:%d", h.ID), Author: jc.Job.Author, Steps: steps,
		Params: VMProvisionParams{HostID: h.ID, Spec: spec, InstallNKT: install, ProfileID: profileID},
	})
	if err != nil {
		return err
	}
	jc.Log("hub.scriptVMJob", st.Name, jobID)
	if err := r.waitHubJob(ctx, jc, jobID); err != nil {
		return err
	}
	// Машина заведена хостом с тем же именем — находим её.
	hosts, err := r.m.db.ListHosts(ctx)
	if err != nil {
		return err
	}
	for _, vm := range hosts {
		if vm.Name == st.Name && vm.ParentID == h.ID {
			done.HostIDs[st.Name] = vm.ID
			jc.SaveResume(*done)
			return nil
		}
	}
	return nil
}

// waitHubJob ждёт задание самого хаба, пересказывая его журнал.
func (r *ScriptRunner) waitHubJob(ctx context.Context, jc *jobs.Context, jobID int64) error {
	deadline := time.Now().Add(scriptStepTimeout)
	var after int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lines, err := r.m.db.JobLog(ctx, jobID, after, 200)
		if err == nil {
			for _, l := range lines {
				jc.Logf("      %s", l.Text)
				after = l.Seq
			}
		}
		job, err := r.m.db.JobByID(ctx, jobID)
		if err != nil {
			return err
		}
		if job.Done() {
			if job.Status != store.JobSucceeded {
				return msgs.Errorf("hub.jobOnHost", job.Status, job.Error)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.jobOnHostDidFinish", scriptStepTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func (r *ScriptRunner) profileByName(ctx context.Context, name string) (store.Profile, error) {
	list, err := r.m.db.ListProfiles(ctx)
	if err != nil {
		return store.Profile{}, err
	}
	for _, p := range list {
		if p.Name == name {
			return r.m.db.ProfileByID(ctx, p.ID)
		}
	}
	return store.Profile{}, msgs.Errorf("script.unknownProfile", name)
}

func (r *ScriptRunner) profileID(ctx context.Context, name string) (int64, error) {
	p, err := r.profileByName(ctx, name)
	if err != nil {
		return 0, err
	}
	return p.ID, nil
}

func (r *ScriptRunner) logTail(jc *jobs.Context, out string) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 12 {
		lines = lines[len(lines)-12:]
	}
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			jc.Logf("      %s", l)
		}
	}
}

// stackName — имя стека из пути: каталог compose-файла.
func stackName(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return "stack"
}
