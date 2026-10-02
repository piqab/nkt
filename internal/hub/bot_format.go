package hub

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // часовые пояса ботов — и в контейнере без tzdata

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Оформление сообщений ботов: события блоками (жирная шапка, находки
// списком), время в часовом поясе бота, хосты с цветом по состоянию.
// Разметку делает платформа: Telegram — HTML, Slack — mrkdwn.

// botPart — блок сообщения: шапка (жирным) и текст.
type botPart struct {
	Head string
	Body string
}

func (r botReply) parts() []botPart {
	if len(r.Parts) > 0 {
		return r.Parts
	}
	return []botPart{{Body: r.Text}}
}

// plain — текст без разметки (журнал, тесты, запасной вид).
func (r botReply) plain() string {
	var out []string
	for _, p := range r.parts() {
		out = append(out, strings.TrimSpace(p.Head+"\n"+p.Body))
	}
	return strings.Join(out, "\n\n")
}

// telegramHTML — для parse_mode HTML: шапка жирным, остальное экранировано.
func (r botReply) telegramHTML() string {
	var out []string
	for _, p := range r.parts() {
		var b strings.Builder
		if p.Head != "" {
			b.WriteString("<b>" + html.EscapeString(p.Head) + "</b>")
			if p.Body != "" {
				b.WriteString("\n")
			}
		}
		b.WriteString(html.EscapeString(trimRunes(p.Body, 3000/len(r.parts()))))
		out = append(out, b.String())
	}
	return strings.Join(out, "\n\n")
}

// slackMrkdwn — для Slack: шапка *жирным*, &, <, > — сущностями.
func (r botReply) slackMrkdwn() string {
	var out []string
	for _, p := range r.parts() {
		text := slackEscape(trimRunes(p.Body, 2800/len(r.parts())))
		if p.Head != "" {
			text = strings.TrimSpace("*" + slackEscape(p.Head) + "*\n" + text)
		}
		out = append(out, text)
	}
	return strings.Join(out, "\n\n")
}

// --- время ------------------------------------------------------------------------

// hubZone — часовой пояс машины хаба: имя и смещение («Asia/Yekaterinburg,
// UTC+05:00»).
func hubZone() string {
	name := os.Getenv("TZ")
	if name == "" {
		if target, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
			if i := strings.Index(target, "zoneinfo/"); i >= 0 {
				name = target[i+len("zoneinfo/"):]
			}
		}
	}
	if name == "" {
		name = time.Local.String()
	}
	return name + ", UTC" + time.Now().Format("-07:00")
}

// botLocation — свой пояс бота или пояс хаба.
func botLocation(name string) *time.Location {
	if name != "" {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.Local
}

// validZone — пояс из настроек бота (пусто — как у хаба).
func validZone(name string) bool {
	if name == "" {
		return true
	}
	_, err := time.LoadLocation(name)
	return err == nil && !strings.ContainsAny(name, " \n")
}

// botTime — «13:05» сегодня, «вчера 22:40», «30.09 08:15» раньше.
func botTime(ts time.Time, loc *time.Location, lang msgs.Lang, now time.Time) string {
	if loc == nil {
		loc = time.Local
	}
	t, n := ts.In(loc), now.In(loc)
	day := func(x time.Time) time.Time { y, m, d := x.Date(); return time.Date(y, m, d, 0, 0, 0, 0, loc) }
	switch {
	case day(t).Equal(day(n)):
		return t.Format("15:04")
	case day(t).Equal(day(n).AddDate(0, 0, -1)):
		return msgs.T(lang, "tg.yesterday") + " " + t.Format("15:04")
	case lang == msgs.EN:
		return t.Format("Jan 2 15:04")
	}
	return t.Format("02.01 15:04")
}

// --- события ----------------------------------------------------------------------

var botEventIcon = map[string]string{store.EventUnreachable: "🔴", store.EventRecovered: "🟢", store.EventProblems: "⚠️", store.EventResolved: "✅",
	store.EventJobFailed: "❌", store.EventRebooted: "🔄", store.EventBans: "🚫", OutDeploySucceeded: "🚀", OutDeployFailed: "💥"}

// eventBody — текст события; новые находки — заголовком и списком (до
// трёх, дальше «…и ещё N»). Разбивка по аргументам события — годится и
// для старых записей журнала.
func eventBody(lang msgs.Lang, key, args, text string) string {
	if key == "hub.seriousFindingsNowNamed" {
		if a := msgs.DecodeArgs(args); len(a) == 3 {
			now, _ := strconv.Atoi(fmt.Sprint(a[0]))
			was, _ := strconv.Atoi(fmt.Sprint(a[1]))
			names := slices.DeleteFunc(strings.Split(fmt.Sprint(a[2]), "; "), func(s string) bool { return s == "…" || s == "" })
			lines := []string{msgs.T(lang, "tg.findingsCount", now, was)}
			for i, n := range names {
				if i == 3 {
					break
				}
				lines = append(lines, "• "+n)
			}
			if more := max(len(names), now-was) - min(len(names), 3); more > 0 {
				lines = append(lines, msgs.T(lang, "tg.andMore", more))
			}
			return strings.Join(lines, "\n")
		}
	}
	return msgs.Render(lang, key, args, text)
}

// eventHead — «⚠️ web1 — новые проблемы · 13:05».
func eventHead(lang msgs.Lang, loc *time.Location, kind string, who []string, ts time.Time) string {
	head := strings.TrimSpace(botEventIcon[kind] + " " + strings.Join(who, ", "))
	if len(who) > 0 {
		head += " — "
	}
	head += msgs.T(lang, "tg.kind."+kind)
	if !ts.IsZero() {
		head += " · " + botTime(ts, loc, lang, time.Now())
	}
	return head
}

// alerts — последние оповещения; одинаковые (вид и текст) в пределах двух
// минут — одним блоком с несколькими хостами. Кнопки — «Проблемы» хостов.
func (c *botCore) alerts(ctx context.Context, t botTurn) botReply {
	lang := t.Lang
	res, err := c.s.hub.QueryEvents(msgs.WithLang(ctx, lang), EventQuery{Limit: 40})
	if err != nil {
		return botReply{Text: msgs.Localize(lang, err)}
	}
	if len(res.Events) == 0 {
		return botReply{Text: msgs.T(lang, "tg.noAlerts")}
	}
	type group struct {
		kind, body string
		ts         time.Time
		hosts      []string
		ids        []int64
	}
	var groups []*group
	for _, e := range res.Events {
		ts, _ := time.Parse(time.RFC3339, e.TS)
		body := eventBody(lang, e.DetailKey, e.DetailArgs, e.Detail)
		if n := len(groups); n > 0 {
			g := groups[n-1]
			if g.kind == e.Kind && g.body == body && g.ts.Sub(ts) <= 2*time.Minute {
				if !slices.Contains(g.hosts, e.HostName) {
					g.hosts = append(g.hosts, e.HostName)
					g.ids = append(g.ids, e.HostID)
				}
				continue
			}
		}
		if len(groups) == 5 {
			break
		}
		groups = append(groups, &group{kind: e.Kind, body: body, ts: ts, hosts: []string{e.HostName}, ids: []int64{e.HostID}})
	}
	out := botReply{}
	var seen []int64
	for _, g := range groups {
		out.Parts = append(out.Parts, botPart{Head: eventHead(lang, t.Loc, g.kind, g.hosts, g.ts), Body: g.body})
		for i, id := range g.ids {
			if len(out.Buttons) < 5 && !slices.Contains(seen, id) && g.kind != store.EventRecovered && g.kind != store.EventResolved {
				seen = append(seen, id)
				out.Buttons = append(out.Buttons, botButton{"⚠️ " + g.hosts[i], "fd:" + strconv.FormatInt(id, 10)})
			}
		}
	}
	return out
}

// notifyReply — оповещение для чата: блок с шапкой и кнопки (тест не
// шлётся).
func (c *botCore) notifyReply(lang msgs.Lang, loc *time.Location, kinds []string, ev OutEvent) (botReply, bool) {
	if ev.Kind == OutTest || (len(kinds) > 0 && !slices.Contains(kinds, ev.Kind)) {
		return botReply{}, false
	}
	who := []string{}
	if ev.HostName != "" {
		who = append(who, ev.HostName)
	} else if ev.PipelineName != "" {
		who = append(who, ev.PipelineName)
	}
	r := botReply{Parts: []botPart{{Head: eventHead(lang, loc, ev.Kind, who, ev.TS), Body: eventBody(lang, ev.Key, ev.Args, ev.Text)}}}
	switch {
	case ev.HostName != "":
		r.Buttons = append(r.Buttons, botButton{msgs.T(lang, "tg.btnOverview"), "ov:" + strconv.FormatInt(ev.HostID, 10)})
		if ev.Kind == store.EventProblems {
			r.Buttons = append(r.Buttons, botButton{msgs.T(lang, "tg.btnFindings"), "fd:" + strconv.FormatInt(ev.HostID, 10)})
		}
	case ev.PipelineID != 0:
		r.Buttons = append(r.Buttons, botButton{msgs.T(lang, "tg.btnLog"), "jl:" + strconv.FormatInt(ev.JobID, 10)})
		if ev.Kind == OutDeployFailed {
			r.Buttons = append(r.Buttons, botButton{msgs.T(lang, "tg.btnRetry"), "rd:" + strconv.FormatInt(ev.PipelineID, 10)})
		}
	}
	return r, true
}

// --- хосты ------------------------------------------------------------------------

// hostState — значок, порядок (худшие первыми) и что сказать о хосте.
func hostState(lang msgs.Lang, h hostWithOverview) (string, int, string) {
	f := h.Findings
	switch {
	case h.Status == store.HostStatusError:
		msg := strings.TrimSpace(strings.SplitN(h.ErrorMsg, "\n", 2)[0])
		return "🔴", 0, msgs.T(lang, "tg.hostError", trimRunes(msg, 80))
	case h.Reachable != nil && !*h.Reachable:
		return "🔴", 0, msgs.T(lang, "tg.hostDown")
	case h.Status == store.HostStatusInstalling:
		return "⚪", 4, msgs.T(lang, "tg.hostInstalling")
	case h.Reachable == nil:
		return "⚪", 4, msgs.T(lang, "tg.hostNoData")
	case f["critical"]+f["high"] > 0:
		return "🟠", 1, findingsText(lang, f)
	case f["medium"] > 0:
		return "🟡", 2, findingsText(lang, f)
	}
	return "🟢", 3, findingsText(lang, f)
}

func findingsText(lang msgs.Lang, f map[string]int) string {
	if f["critical"]+f["high"]+f["medium"] == 0 {
		return msgs.T(lang, "tg.noFindings")
	}
	return msgs.T(lang, "tg.findings", f["critical"], f["high"], f["medium"])
}

func (c *botCore) hosts(ctx context.Context, lang msgs.Lang) botReply {
	rows, err := c.s.hostRows(ctx)
	if err != nil {
		return botReply{Text: msgs.Localize(lang, err)}
	}
	type line struct {
		rank int
		name string
		text string
	}
	var lines []line
	for _, h := range rows {
		icon, rank, what := hostState(lang, h)
		lines = append(lines, line{rank, h.Name, fmt.Sprintf("%s %s — %s", icon, h.Name, what)})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].rank != lines[j].rank {
			return lines[i].rank < lines[j].rank
		}
		return lines[i].name < lines[j].name
	})
	var out []string
	for i, l := range lines {
		if i == 40 {
			out = append(out, msgs.T(lang, "tg.andMore", len(lines)-40))
			break
		}
		out = append(out, l.text)
	}
	return botReply{Parts: []botPart{{Head: msgs.T(lang, "tg.hostsHead", len(rows)), Body: strings.Join(out, "\n")}}}
}

func (c *botCore) status(ctx context.Context, lang msgs.Lang) botReply {
	rows, err := c.s.hostRows(ctx)
	if err != nil {
		return botReply{Text: msgs.Localize(lang, err)}
	}
	counts := map[string]int{}
	findings := map[string]int{}
	for _, h := range rows {
		icon, _, _ := hostState(lang, h)
		counts[icon]++
		for k, v := range h.Findings {
			findings[k] += v
		}
	}
	return botReply{Parts: []botPart{{
		Head: msgs.T(lang, "tg.hostsHead", len(rows)),
		Body: msgs.T(lang, "tg.status", counts["🔴"], counts["🟠"], counts["🟡"], counts["🟢"], counts["⚪"],
			findings["critical"], findings["high"], findings["medium"]),
	}}}
}
