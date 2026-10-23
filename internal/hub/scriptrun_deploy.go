package hub

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Команды сценария вокруг выкладок: «pipeline» (описание конвейера
// блоком), «deploy» (выкладка с ожиданием итога), «migrate» (перенос
// стека, как с карты хостов) и «wait url» (адрес снаружи — запросом с
// хаба). Всё — теми же функциями, что кнопки интерфейса.

// pipelineByName — конвейер хаба по имени.
func (r *ScriptRunner) pipelineByName(ctx context.Context, name string) (store.Pipeline, bool) {
	list, err := r.m.db.ListPipelines(ctx)
	if err != nil {
		return store.Pipeline{}, false
	}
	for _, p := range list {
		if p.Name == name {
			return p, true
		}
	}
	return store.Pipeline{}, false
}

// savePipeline — «pipeline ИМЯ … end»: создать конвейер или обновить
// описание, как «Сохранить» в окне конвейера: значения блока env уходят
// в .env конвейера, generate: создаёт хаб (у уже заданных — пропуск).
// Значение, которое отличается от уже заданного, молча не заменяется —
// ошибка с подсказкой («Секреты» конвейера).
func (r *ScriptRunner) savePipeline(ctx context.Context, jc *jobs.Context, st script.Step) error {
	s := r.s
	user := jc.Job.Author
	content := st.Block
	if _, err := deploy.ParseSpec(content); err != nil {
		return err
	}
	if p, ok := r.pipelineByName(ctx, st.Name); ok {
		cur, err := s.pipelineEnv(p)
		if err != nil {
			return err
		}
		move, err := planEnvMove(content, cur, false)
		if err != nil {
			return err
		}
		if len(move.Conflicts) > 0 {
			return msgs.Errorf("hub.scriptPipelineEnvConflict", st.Name, strings.Join(move.Conflicts, ", "))
		}
		s.rememberDeployedBefore(ctx, p)
		note := msgs.T(jc.Lang(), "hub.scriptPipelineNote", jc.Job.ID)
		err = s.db.UpdatePipelineContent(ctx, p.ID, move.Content, user, note)
		s.db.Audit(ctx, user, "pipeline.update", p.Name, auditOutcome(err), envAuditNote(note, move))
		if err != nil {
			return err
		}
		if err := s.pipelineEnvAfterSave(ctx, p.ID, user, move); err != nil {
			return err
		}
		jc.Log("hub.scriptPipelineUpdated", st.Name)
		return nil
	}
	move, err := planEnvMove(content, nil, false)
	if err != nil {
		return err
	}
	secret, err := secretbox.Encrypt(r.m.key, []byte(randomToken(32)))
	if err != nil {
		return err
	}
	id, err := s.db.CreatePipeline(ctx, store.Pipeline{Name: st.Name, Content: move.Content, HookID: randomHex(16), HookSecret: secret, Author: user})
	s.db.Audit(ctx, user, "pipeline.create", st.Name, auditOutcome(err), envAuditDetail(move))
	if err != nil {
		return err
	}
	if err := s.pipelineEnvAfterSave(ctx, id, user, move); err != nil {
		return err
	}
	jc.Log("hub.scriptPipelineCreated", st.Name)
	return nil
}

// runDeploy — «deploy ИМЯ»: выкладка конвейера тем же путём, что кнопка
// «Выложить», и ожидание итога (журнал выкладки — в журнал сценария).
func (r *ScriptRunner) runDeploy(ctx context.Context, jc *jobs.Context, st script.Step) error {
	p, ok := r.pipelineByName(ctx, st.Name)
	if !ok {
		return msgs.Errorf("script.unknownPipeline", st.Name)
	}
	user := jc.Job.Author
	timeout, _ := time.ParseDuration(st.Args["timeout"])
	if timeout <= 0 {
		timeout = scriptStepTimeout
	}
	if action := st.Args["action"]; action != "" {
		// deploy ИМЯ start|stop — та же версия на хостах выкладки, как
		// кнопки «Запустить» и «Остановить».
		id, err := r.s.startPipelinePower(ctx, p, action, user)
		if err != nil {
			return err
		}
		jc.Log("hub.scriptPowerJob", p.Name, action, id)
		return r.waitHubJobFor(ctx, jc, id, timeout)
	}
	d, err := r.s.startDeployment(ctx, p, store.Deployment{Trigger: "script", Author: user}, false)
	r.s.db.Audit(ctx, user, "pipeline.deploy", p.Name, auditOutcome(err), map[string]any{"job_id": d.JobID, "script_job": jc.Job.ID})
	if err != nil {
		return err
	}
	jc.Log("hub.scriptDeployJob", p.Name, d.JobID)
	return r.waitHubJobFor(ctx, jc, d.JobID, timeout)
}

// dryDeploy — «deploy» в сухом прогоне: сухой прогон конвейера (тот же,
// что кнопка в окне), с описанием из «pipeline» выше по сценарию, если
// оно там есть: значения его блока env — в одноразовое хранилище, как у
// несохранённого описания в окне.
func (r *ScriptRunner) dryDeploy(ctx context.Context, jc *jobs.Context, st script.Step, content string) error {
	p, saved := r.pipelineByName(ctx, st.Name)
	if action := st.Args["action"]; action != "" {
		// Запуск/остановка: хосты и маршруты, которые затронет, —
		// ничего не трогая.
		if !saved {
			jc.Log("hub.scriptDryWould", st.Text)
			return nil
		}
		plan, err := r.s.pipelinePowerPlan(ctx, p)
		if err != nil {
			return err
		}
		var hosts []string
		for _, h := range plan.Hosts {
			hosts = append(hosts, h.Name)
		}
		routes := strings.Join(plan.Routes, ", ")
		if routes == "" {
			routes = "—"
		}
		jc.Log("hub.scriptDryPower."+action, p.Name, strings.Join(hosts, ", "), routes)
		return nil
	}
	if content == "" {
		if !saved {
			return msgs.Errorf("script.unknownPipeline", st.Name)
		}
		content = p.Content
	}
	spec, err := deploy.ParseSpec(content)
	if err != nil {
		return err
	}
	if spec.Action != deploy.ActionCompose {
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	var id int64
	if saved {
		id = p.ID
	}
	content, envToken, _, err := r.s.dryEnvOverride(ctx, id, content)
	if err != nil {
		return err
	}
	queue := "deploy:dryrun"
	if id > 0 {
		queue = fmt.Sprintf("deploy:%d", id)
	}
	jobID, err := r.s.jobs.Start(ctx, jobs.Spec{
		Kind: KindDeploy, TitleKey: "deploy.dryTitle", TitleArgs: []any{st.Name},
		Queue: queue, Author: jc.Job.Author, Steps: 3,
		Params: DeployParams{DryRun: true, PipelineID: id, Content: content, EnvToken: envToken},
	})
	if err != nil {
		return err
	}
	jc.Log("hub.scriptDeployJob", st.Name, jobID)
	return r.waitHubJob(ctx, jc, jobID)
}

// runMigrate — «migrate ХОСТ КОНТЕЙНЕР to ХОСТ …»: план переноса (тот же,
// что окно на карте), препятствия — ошибка до остановки стека, затем
// задание переноса с ожиданием. Упавший перенос откатывается сам — шаг
// сценария падает с его ошибкой.
func (r *ScriptRunner) runMigrate(ctx context.Context, jc *jobs.Context, done *scriptRunResume, st script.Step, dry bool) error {
	from, err := r.hostByName(ctx, done, st.Host)
	if err != nil {
		return err
	}
	to, err := r.hostByName(ctx, done, st.Args["to"])
	if err != nil {
		return err
	}
	if from.ID == to.ID {
		return msgs.Errorf("script.migrateSameHost", from.Name)
	}
	if dry && (done.HostIDs[st.Host] == 0 && hasKey(done.HostIDs, st.Host) || done.HostIDs[st.Args["to"]] == 0 && hasKey(done.HostIDs, st.Args["to"])) {
		jc.Log("hub.scriptDryNewHost", st.Host)
		return nil
	}
	user := jc.Job.Author
	// Уже перенесён (сценарий продолжен после перезапуска хаба посреди
	// переноса): последняя запись этого контейнера — сюда.
	if rec := r.lastMigration(ctx, st.Name); rec != nil && rec.State == "moved" && rec.From == from.ID && rec.To == to.ID && !dry {
		jc.Log("hub.scriptMigrateAlready", st.Name, to.Name)
		return nil
	}
	plan, check, err := r.s.migrateInspect(ctx, user, from.ID, to.ID, st.Name)
	if err != nil {
		if dry && done.DeployedInDry {
			// Стек выкладывается выше по этому же сценарию — в сухом
			// прогоне его на хосте ещё нет.
			jc.Log("hub.scriptDryMigrateLater", st.Name, from.Name)
			return nil
		}
		return err
	}
	var names []string
	for _, c := range plan.Containers {
		names = append(names, c.Name)
	}
	var size int64
	for _, v := range plan.Volumes {
		size += v.Size
	}
	for _, p := range plan.Paths {
		size += p.Size
	}
	jc.Log("hub.scriptMigratePlan", plan.Project, strings.Join(names, ", "), float64(size)/(1<<20), from.Name, to.Name)
	clean := st.Args["clean-leftovers"] == "true"
	if clean {
		if rec := r.s.migrateLeftovers(ctx, from.ID, to.ID, plan.Project); rec != nil {
			check, _, _ = withoutLeftovers(check, rec)
		}
	}
	if probs := migrateProblems(plan, check); len(probs) > 0 {
		for _, pr := range probs {
			jc.Log(pr[0], pr[1])
		}
		return msgs.Errorf("migrate.cannot", len(probs))
	}
	for _, t := range r.s.migrateFlyAffected(ctx, from.ID, names) {
		jc.Log("hub.scriptMigrateFly", t.Title, st.Args["switch-fly"] == "true")
	}
	if pv := r.s.migratePipelinePreview(ctx, from.ID, to.ID, plan.Project); pv != nil {
		jc.Log("hub.scriptMigratePipeline", pv.Name, st.Args["switch-pipeline"] == "true" && pv.CanSwitch)
	}
	if dry {
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	p := MigrateParams{Action: "move", From: from.ID, To: to.ID, Container: st.Name,
		SwitchFly: st.Args["switch-fly"] == "true", SwitchPipeline: st.Args["switch-pipeline"] == "true", CleanLeftovers: clean}
	jobID, err := r.s.jobs.Start(ctx, jobs.Spec{
		Kind: KindContainerMigrate, TitleKey: "migrate.title.move", TitleArgs: []any{plan.Project, from.Name, to.Name},
		Queue: migrateHubQueue, Author: user, Steps: 6, Params: p,
	})
	r.s.db.Audit(ctx, user, "migrate.move", plan.Project, auditOutcome(err), map[string]any{"from": from.ID, "to": to.ID, "script_job": jc.Job.ID})
	if err != nil {
		return err
	}
	jc.Log("hub.scriptMigrateJob", plan.Project, jobID)
	return r.waitHubJobFor(ctx, jc, jobID, 3*time.Hour)
}

func hasKey(m map[string]int64, k string) bool {
	_, ok := m[k]
	return ok
}

// lastMigration — последняя запись переноса контейнера name.
func (r *ScriptRunner) lastMigration(ctx context.Context, name string) *migrateRecord {
	var last *migrateRecord
	for _, rec := range r.s.loadMigrations(ctx) {
		if slices.Contains(rec.Containers, name) && (last == nil || rec.TS > last.TS) {
			c := rec
			last = &c
		}
	}
	return last
}

// waitURL — «wait url URL [КОД]»: запрос с хаба по настоящему адресу —
// DNS, шлюз, сертификат проверяются, как у пользователя. Перенаправления
// не идут: ждём ответ именно этого адреса.
func (r *ScriptRunner) waitURL(ctx context.Context, jc *jobs.Context, st script.Step) error {
	timeout, _ := time.ParseDuration(st.Args["timeout"])
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	client := &http.Client{
		Timeout:       15 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, Proxy: http.ProxyFromEnvironment},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	want := st.Args["code"]
	deadline := time.Now().Add(timeout)
	var last string
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, st.Args["url"], nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if strconv.Itoa(resp.StatusCode) == want {
				jc.Log("hub.scriptWaitURLOK", st.Args["url"], resp.StatusCode)
				return nil
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.scriptWaitFailed", st.Text, timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
