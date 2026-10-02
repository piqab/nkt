package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Ядро ботов (Telegram, Slack): команды, кнопки, подтверждения, тексты.
// Платформа — только транспорт: как принять команду и нажатие, как
// отправить сообщение с кнопками. Права решает платформа по своим
// настройкам (чат или канал с ролью, список людей) и передаёт сюда.

type botButton struct {
	Text string
	Data string
}

// botReply — сообщение бота: текст и ряд кнопок.
type botReply struct {
	// Text — простое сообщение; Parts — блоки с жирной шапкой (события,
	// списки). Есть Parts — Text не используется.
	Text    string
	Parts   []botPart
	Buttons []botButton
}

// botTurn — кто пишет и что ему можно.
type botTurn struct {
	Platform string // telegram, slack
	User     string // id на платформе
	UserName string
	// CanAct — действия (выкладка, сухой прогон, бан): роль чата и список
	// людей уже проверены платформой.
	CanAct bool
	Lang   msgs.Lang
	// Loc — часовой пояс бота (свой в настройках или как у хаба).
	Loc *time.Location
	// Later — отправить в тот же чат позже (итог задания).
	Later func(botReply)
}

func (t botTurn) author() string { return t.Platform + ":" + t.UserName }

type botPending struct {
	action, arg, user string
	at                time.Time
}

type botCore struct {
	s       *Server
	mu      sync.Mutex
	pending map[string]botPending
}

func newBotCore(s *Server) *botCore { return &botCore{s: s, pending: map[string]botPending{}} }

var botIPRe = regexp.MustCompile(`^[0-9A-Fa-f:.]{2,45}(/\d{1,3})?$`)

// botHelp — ключи справки платформы.
var botHelp = map[string][2]string{
	"telegram": {"tg.helpRead", "tg.helpAdmin"},
	"slack":    {"slack.helpRead", "slack.helpAdmin"},
}

// command — команда без префикса платформы: status, hosts, deploy…
func (c *botCore) command(ctx context.Context, t botTurn, cmd string, args []string) []botReply {
	lang := t.Lang
	one := func(text string, buttons ...botButton) []botReply {
		return []botReply{{Text: text, Buttons: buttons}}
	}
	switch cmd {
	case "start", "help", "":
		keys := botHelp[t.Platform]
		if t.CanAct {
			return one(msgs.T(lang, keys[1]))
		}
		return one(msgs.T(lang, keys[0]))
	case "status":
		return []botReply{c.status(ctx, lang)}
	case "hosts":
		return []botReply{c.hosts(ctx, lang)}
	case "alerts":
		return []botReply{c.alerts(ctx, t)}
	case "pipelines":
		return one(c.pipelines(ctx, lang))
	case "deploy", "dryrun":
		if !t.CanAct {
			return one(msgs.T(lang, "tg.denied"))
		}
		pl, ok := c.findPipeline(ctx, strings.Join(args, " "))
		if !ok {
			return one(msgs.T(lang, "tg.noPipeline", strings.Join(args, " ")))
		}
		if cmd == "dryrun" {
			return one(c.dryRun(ctx, t, pl))
		}
		key := c.ask("deploy", strconv.FormatInt(pl.ID, 10), t.User)
		return one(msgs.T(lang, "tg.confirmDeploy", pl.Name), botButton{msgs.T(lang, "tg.btnDeploy"), "ok:" + key}, botButton{msgs.T(lang, "tg.btnCancel"), "no:" + key})
	case "ban", "unban":
		if !t.CanAct {
			return one(msgs.T(lang, "tg.denied"))
		}
		if len(args) != 1 || !botIPRe.MatchString(args[0]) {
			return one(msgs.T(lang, "tg.banUsage"))
		}
		ip, err := fail2ban.ParseIP(args[0])
		if err != nil {
			return one(msgs.Localize(lang, err))
		}
		action, ask, btn := "ban", "tg.confirmBan", "tg.btnBan"
		if cmd == "unban" {
			action, ask, btn = "unban", "tg.confirmUnban", "tg.btnUnban"
		}
		key := c.ask(action, ip.String(), t.User)
		return one(msgs.T(lang, ask, ip.String()), botButton{msgs.T(lang, btn), "ok:" + key}, botButton{msgs.T(lang, "tg.btnCancel"), "no:" + key})
	}
	return one(msgs.T(lang, "tg.unknownCommand"))
}

// press — нажатие кнопки (данные кнопки — «вид:аргумент»).
func (c *botCore) press(ctx context.Context, t botTurn, data string) []botReply {
	lang := t.Lang
	one := func(text string, buttons ...botButton) []botReply {
		return []botReply{{Text: text, Buttons: buttons}}
	}
	kind, arg, _ := strings.Cut(data, ":")
	switch kind {
	case "ov", "fd":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil
		}
		return []botReply{c.hostDetail(ctx, id, kind == "fd", lang)}
	case "jl":
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil
		}
		return one(c.jobTail(ctx, id, 15, lang))
	case "rd":
		if !t.CanAct {
			return one(msgs.T(lang, "tg.denied"))
		}
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil {
			return nil
		}
		pl, err := c.s.db.PipelineByID(ctx, id)
		if err != nil {
			return nil
		}
		key := c.ask("deploy", arg, t.User)
		return one(msgs.T(lang, "tg.confirmDeploy", pl.Name), botButton{msgs.T(lang, "tg.btnDeploy"), "ok:" + key}, botButton{msgs.T(lang, "tg.btnCancel"), "no:" + key})
	case "no":
		if _, ok := c.take(arg, t.User); ok {
			return one(msgs.T(lang, "tg.cancelled"))
		}
	case "ok":
		p, ok := c.take(arg, t.User)
		if !ok {
			return one(msgs.T(lang, "tg.expired"))
		}
		if !t.CanAct {
			return one(msgs.T(lang, "tg.denied"))
		}
		return one(c.act(ctx, t, p))
	}
	return nil
}

// ask — подтверждение действия: ключ кнопки (живёт 10 минут, только для
// того же человека).
func (c *botCore) ask(action, arg, user string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, p := range c.pending {
		if time.Since(p.at) > 10*time.Minute {
			delete(c.pending, k)
		}
	}
	key := randomHex(6)
	c.pending[key] = botPending{action: action, arg: arg, user: user, at: time.Now()}
	return key
}

func (c *botCore) take(key, user string) (botPending, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pending[key]
	if !ok || p.user != user || time.Since(p.at) > 10*time.Minute {
		return botPending{}, false
	}
	delete(c.pending, key)
	return p, true
}

// act — подтверждённое действие; итог задания придёт позже.
func (c *botCore) act(ctx context.Context, t botTurn, p botPending) string {
	s, lang, author := c.s, t.Lang, t.author()
	switch p.action {
	case "deploy":
		id, _ := strconv.ParseInt(p.arg, 10, 64)
		pl, err := s.db.PipelineByID(ctx, id)
		if err != nil {
			return msgs.Localize(lang, err)
		}
		d, err := s.startDeployment(ctx, pl, store.Deployment{Trigger: t.Platform, Author: author}, true)
		s.db.Audit(ctx, author, "pipeline.deploy", pl.Name, auditOutcome(err), map[string]any{"via": t.Platform})
		if err != nil {
			return msgs.Localize(lang, err)
		}
		go c.watchJob(context.WithoutCancel(ctx), t, d.JobID)
		return msgs.T(lang, "tg.deployStarted", pl.Name, d.JobID)
	case "ban", "unban":
		params := F2BFleetParams{Action: p.action, IPs: []string{p.arg}}
		targets, err := s.f2bTargets(ctx, nil)
		if err == nil && len(targets) == 0 {
			err = msgs.Errorf("hub.f2bNoHosts")
		}
		if err != nil {
			return msgs.Localize(lang, err)
		}
		for _, h := range targets {
			params.HostIDs = append(params.HostIDs, h.ID)
		}
		titleKey := "hub.f2bJobBan"
		if p.action == "unban" {
			titleKey = "hub.f2bJobUnban"
		}
		id, err := s.jobs.Start(ctx, jobsSpecF2B(titleKey, author, params))
		s.db.Audit(ctx, author, "fail2ban.fleet", p.arg, auditOutcome(err), map[string]any{"action": p.action, "via": t.Platform})
		if err != nil {
			return msgs.Localize(lang, err)
		}
		go c.watchJob(context.WithoutCancel(ctx), t, id)
		return msgs.T(lang, "tg.jobStarted", id)
	}
	return ""
}

func (c *botCore) dryRun(ctx context.Context, t botTurn, pl store.Pipeline) string {
	author := t.author()
	id, err := c.s.jobs.Start(ctx, jobsSpecDry(pl, author))
	c.s.db.Audit(ctx, author, "pipeline.dryrun", pl.Name, auditOutcome(err), map[string]any{"via": t.Platform})
	if err != nil {
		return msgs.Localize(t.Lang, err)
	}
	go c.watchJob(context.WithoutCancel(ctx), t, id)
	return msgs.T(t.Lang, "tg.dryStarted", pl.Name, id)
}

// watchJob — итог задания в тот же чат (до получаса).
func (c *botCore) watchJob(ctx context.Context, t botTurn, id int64) {
	if t.Later == nil {
		return
	}
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		j, err := c.s.db.JobByID(ctx, id)
		if err != nil {
			return
		}
		if j.Status != store.JobQueued && j.Status != store.JobRunning {
			t.Later(botReply{Text: c.jobTail(ctx, id, 12, t.Lang)})
			return
		}
		time.Sleep(3 * time.Second)
	}
}

// --- тексты ---------------------------------------------------------------------

func (c *botCore) pipelines(ctx context.Context, lang msgs.Lang) string {
	list, err := c.s.db.ListPipelines(ctx)
	if err != nil {
		return msgs.Localize(lang, err)
	}
	if len(list) == 0 {
		return msgs.T(lang, "tg.noPipelines")
	}
	var lines []string
	for _, p := range list {
		last := "—"
		if ds, err := c.s.db.Deployments(ctx, p.ID, 1); err == nil && len(ds) > 0 {
			last = ds[0].Status
		}
		lines = append(lines, fmt.Sprintf("%d · %s — %s", p.ID, p.Name, last))
	}
	return strings.Join(lines, "\n")
}

func (c *botCore) findPipeline(ctx context.Context, arg string) (store.Pipeline, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return store.Pipeline{}, false
	}
	if id, err := strconv.ParseInt(arg, 10, 64); err == nil {
		p, err := c.s.db.PipelineByID(ctx, id)
		return p, err == nil
	}
	list, _ := c.s.db.ListPipelines(ctx)
	for _, p := range list {
		if strings.EqualFold(p.Name, arg) {
			return p, true
		}
	}
	return store.Pipeline{}, false
}

func (c *botCore) jobTail(ctx context.Context, id int64, n int, lang msgs.Lang) string {
	j, err := c.s.db.JobByID(ctx, id)
	if err != nil {
		return msgs.T(lang, "tg.noJob", id)
	}
	lines, _ := c.s.db.JobLog(ctx, id, 0, 2000)
	var tail []string
	for _, l := range lines {
		tail = append(tail, msgs.Render(lang, l.Key, l.Args, l.Text))
	}
	if len(tail) > n {
		tail = tail[len(tail)-n:]
	}
	title := msgs.Render(lang, j.TitleKey, j.TitleArgs, j.Title)
	head := msgs.T(lang, "tg.job", id, title, msgs.T(lang, "tg.jobStatus."+j.Status))
	if j.Status == store.JobFailed {
		head += "\n" + msgs.Render(lang, j.ErrorKey, j.ErrorArgs, j.Error)
	}
	return head + "\n\n" + strings.Join(tail, "\n")
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// jobsSpecF2B — задание бана на хостах (как из «fail2ban» хаба).
func jobsSpecF2B(titleKey, author string, p F2BFleetParams) jobs.Spec {
	return jobs.Spec{Kind: KindF2BFleet, TitleKey: titleKey, TitleArgs: []any{strings.Join(p.IPs, ", "), len(p.HostIDs)},
		Author: author, Steps: len(p.HostIDs), Params: p}
}

// jobsSpecDry — сухой прогон сохранённого конвейера с галочками,
// выбранными администраторами.
func jobsSpecDry(pl store.Pipeline, author string) jobs.Spec {
	var skip []string
	_ = json.Unmarshal([]byte(pl.DrySkip), &skip)
	return jobs.Spec{Kind: KindDeploy, TitleKey: "deploy.dryTitle", TitleArgs: []any{pl.Name},
		Queue: fmt.Sprintf("deploy:%d", pl.ID), Author: author, Steps: 3,
		Params: DeployParams{DryRun: true, PipelineID: pl.ID, Content: pl.Content, Skip: skip}}
}

// botCore — общее ядро ботов хаба (подтверждения одни на все платформы).
func (s *Server) botCore() *botCore {
	s.botOnce.Do(func() { s.bot = newBotCore(s) })
	return s.bot
}
