package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// Оповещения рождаются здесь, в фоновом опросе, а не в браузере.
//
// Всплывающее уведомление живёт, пока открыта вкладка: закрыл — и не
// узнал, что ночью отвалился хост. Хаб опрашивает хосты всё время, и
// именно он знает о переходах; браузер теперь только показывает то, что
// хаб записал, и потому всплывающее и журнал не могут разойтись.

// eventKeep — сколько событий хранится. Журнал не архив: смысл в том,
// что случилось за последние дни, а не за всё время.
const eventKeep = 2000

// recordEvent записывает оповещение о хосте.
//
// Ошибка записи не должна ронять опрос: журнал — дополнение к состоянию,
// а не само состояние.
// recordEventMsg — событие с текстом из каталога: ключ и аргументы
// хранятся рядом, чтобы журнал читался на языке смотрящего.
func (m *Manager) recordEventMsg(ctx context.Context, host store.Host, kind, severity, key string, args ...any) {
	m.recordEventKey(ctx, host, kind, severity, msgs.Tc(ctx, key, args...), key, msgs.EncodeArgs(args), "")
}

// recordEventLink — то же со ссылкой в раздел хоста, где событие видно
// и решается (кнопка «К хосту» в журнале).
func (m *Manager) recordEventLink(ctx context.Context, host store.Host, kind, severity, link, key string, args ...any) {
	m.recordEventKey(ctx, host, kind, severity, msgs.Tc(ctx, key, args...), key, msgs.EncodeArgs(args), link)
}

func (m *Manager) recordEvent(ctx context.Context, host store.Host, kind, severity, detail string) {
	m.recordEventKey(ctx, host, kind, severity, detail, "", "", "")
}

// eventLink — путь раздела хоста с ?focus= (несколько значений — через
// запятую, не больше пяти: ссылка, а не список).
func eventLink(path string, focus []string) string {
	if len(focus) > 5 {
		focus = focus[:5]
	}
	if len(focus) == 0 {
		return path
	}
	return path + "?" + url.Values{"focus": {strings.Join(focus, ",")}}.Encode()
}

func (m *Manager) recordEventKey(ctx context.Context, host store.Host, kind, severity, detail, key, args, link string) {
	// Исходящие вебхуки — независимо от записи в журнал (выбор событий у
	// адресата свой).
	var eventID int64
	defer func() {
		m.emitOut(outFromHostEvent(host, hostAddrLabel(ctx, host), kind, severity, detail, key, args, eventID))
	}()
	settings := m.EventSettings(ctx)
	if !settings.Record[kind] {
		return
	}
	if kind == store.EventRecovered && settings.CollapseMinutes > 0 && m.collapseOutage(ctx, host, settings.CollapseMinutes) {
		return
	}
	id, err := m.db.AddHostEvent(ctx, store.HostEvent{
		HostID: host.ID, HostName: host.Name, HostAddr: hostAddrLabel(ctx, host),
		Kind: kind, Severity: severity, Detail: detail, DetailKey: key, DetailArgs: args, Link: link,
	})
	if err != nil {
		m.log.Warn("не удалось записать оповещение", "host", host.Name, "kind", kind, "err", err)
		return
	}
	eventID = id
	if err := m.db.PruneHostEvents(ctx, eventKeep); err != nil {
		m.log.Warn("не удалось подчистить журнал оповещений", "err", err)
	}
}

// collapseOutage сворачивает короткий эпизод: если последнее событие
// этого хоста — «не отвечает» не старше limit минут, оно переписывается в
// «снова отвечает: был недоступен N мин», и отдельной строки не будет.
func (m *Manager) collapseOutage(ctx context.Context, host store.Host, limit int) bool {
	last, ok, err := m.db.LastHostEventFor(ctx, host.ID)
	if err != nil || !ok || last.Kind != store.EventUnreachable {
		return false
	}
	started, err := time.Parse(time.RFC3339, last.TS)
	if err != nil {
		return false
	}
	down := time.Since(started)
	if down > time.Duration(limit)*time.Minute {
		return false
	}
	minutes := int(down.Round(time.Minute) / time.Minute)
	key, args := "hub.unreachableMin", []any{minutes}
	if minutes == 0 {
		key, args = "hub.unreachableUnderMinute", nil
	}
	return m.db.RewriteHostEvent(ctx, last.ID, store.EventRecovered, msgs.Tc(ctx, key, args...), key, msgs.EncodeArgs(args)) == nil
}

// hostAddrLabel — адрес в том виде, в каком его узнают: пользователь и
// порт вместе с адресом. У машины, ещё не получившей адрес, — понятная
// замена вместо заглушки 0.0.0.0.
func hostAddrLabel(ctx context.Context, host store.Host) string {
	if isPlaceholderAddr(host.Addr) {
		return msgs.Tc(ctx, "hub.addrUndetected")
	}
	if host.SSHUser == "" {
		return fmt.Sprintf("%s:%d", host.Addr, host.SSHPort)
	}
	return fmt.Sprintf("%s@%s:%d", host.SSHUser, host.Addr, host.SSHPort)
}

// noteReachability записывает переход «отвечает ↔ не отвечает».
//
// Только переход: хост, лежащий третьи сутки, не должен писать строку
// каждые полминуты.
func (m *Manager) noteReachability(ctx context.Context, hostID int64, reachable bool, reason string) {
	m.overviewMu.Lock()
	prev, seen := m.overview[hostID]
	m.overviewMu.Unlock()
	// Первое наблюдение молчит, только если хост отвечает: это не
	// событие, а начало наблюдения. А вот «не отвечает» при первом же
	// опросе записать надо — иначе перезапуск хаба стирал бы саму память
	// о том, что хост лежит, и в журнале не оставалось бы ничего.
	if seen && prev.reachable == reachable {
		return
	}
	if !seen && reachable {
		return
	}
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return
	}
	if reachable {
		m.recordEventMsg(ctx, host, store.EventRecovered, "", "hub.hostRespondsAgain")
		return
	}
	m.recordEvent(ctx, host, store.EventUnreachable, "", reason)
}

// noteFindings записывает появление и исчезновение серьёзных находок.
//
// Считаются critical и high вместе: разделять их в оповещении незачем —
// смотреть всё равно идут в раздел находок, а строк в журнале станет
// вдвое больше.
func (m *Manager) noteFindings(ctx context.Context, hostID int64, findings map[string]int, severeNow map[string]string) {
	m.overviewMu.Lock()
	prev, seen := m.overview[hostID]
	m.overviewMu.Unlock()
	if !seen || prev.findings == nil {
		return
	}
	was := severe(prev.findings)
	now := severe(findings)
	if was == now {
		return
	}
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return
	}
	switch {
	case now > was:
		// Какие именно: те, чьих ID в прошлом опросе не было. Хост отдаёт
		// только верхушку списка, так что для лавины находок будут
		// названы первые, а счётчик — точный.
		var names, ids []string
		for id, title := range severeNow {
			if _, old := prev.severe[id]; !old {
				names = append(names, title)
				ids = append(ids, id)
			}
		}
		sort.Strings(names)
		sort.Strings(ids)
		if len(names) > 5 {
			names = append(names[:5], "…")
		}
		// Ссылка — в «Проблемы» хоста, новые находки подсвечены.
		link := eventLink("/findings", ids)
		if len(names) > 0 {
			m.recordEventLink(ctx, host, store.EventProblems, "critical+high", link, "hub.seriousFindingsNowNamed", now, was, strings.Join(names, "; "))
		} else {
			m.recordEventLink(ctx, host, store.EventProblems, "critical+high", link, "hub.seriousFindingsNow", now, was)
		}
	case now == 0:
		m.recordEventLink(ctx, host, store.EventResolved, "critical+high", "/findings", "hub.seriousFindingsLeft", was)
	}
}

// noteUptime записывает перезагрузку хоста: аптайм монотонно растёт,
// поэтому значение меньше прежнего бывает только после старта машины —
// ложных срабатываний тут нет по построению. Хост без аптайма (старая
// версия nkt на нём, снимок без /proc/uptime) молчит.
func (m *Manager) noteUptime(ctx context.Context, hostID int64, uptimeS int64) {
	if uptimeS <= 0 {
		return
	}
	m.overviewMu.Lock()
	prev, seen := m.overview[hostID]
	m.overviewMu.Unlock()
	if !seen || prev.uptimeS <= 0 || uptimeS >= prev.uptimeS {
		return
	}
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return
	}
	m.recordEventMsg(ctx, host, store.EventRebooted, "", "hub.hostRebooted",
		uptimeText(uptimeS), uptimeText(prev.uptimeS))
}

// uptimeText — длительность ключом каталога («27 дн.», «5 ч», «3 мин»):
// оповещение читают глазами, секунды в нём не нужны, а язык — того, кто
// читает (аргумент подставляется в hub.hostRebooted как вложенное
// сообщение, см. msgs.Err).
func uptimeText(secs int64) *msgs.Err {
	switch {
	case secs >= 86400:
		return &msgs.Err{Key: "topology.days", Args: []any{int(secs / 86400)}}
	case secs >= 3600:
		return &msgs.Err{Key: "topology.hours", Args: []any{int(secs / 3600)}}
	case secs >= 60:
		return &msgs.Err{Key: "topology.minutes", Args: []any{int(secs / 60)}}
	default:
		return &msgs.Err{Key: "topology.seconds", Args: []any{int(secs)}}
	}
}

// severe — сколько находок, ради которых стоит будить человека.
func severe(findings map[string]int) int {
	return findings["critical"] + findings["high"]
}

// NoteJobFailed записывает неудачу фонового задания хаба — создание
// машины, раскатка профиля по группе. Такое задание идёт минутами, и
// узнавать о его исходе, сидя на странице заданий, приходилось бы
// специально.
func (m *Manager) NoteJobFailed(ctx context.Context, hostID int64, title, reason string) {
	host := store.Host{Name: title}
	if hostID != 0 {
		if h, err := m.db.HostByID(ctx, hostID); err == nil {
			host = h
		}
	}
	m.recordEvent(ctx, host, store.EventJobFailed, "", reason)
}

// Events отдаёт журнал (все виды, включая скрытые) и число непоказанных
// событий.
func (m *Manager) Events(ctx context.Context, limit int) ([]store.HostEvent, int, error) {
	res, err := m.QueryEvents(ctx, EventQuery{Limit: limit, ShowHidden: true})
	return res.Events, res.Unread, err
}

// EventQuery — выборка журнала: виды, хост (по имени — хост могли
// удалить), текст (имя, адрес, подробности), скрытые виды.
type EventQuery struct {
	Limit      int
	Kinds      []string
	Host       string
	Text       string
	ShowHidden bool
	// After — только события новее этого номера (опрос из n8n и скриптов).
	After int64
	// Allow — хосты в пределах токена (nil — все).
	Allow func(hostID int64) bool
}

// EventsResult — выборка, сколько совпало всего, непрочитанные (без
// скрытых видов) и имена хостов журнала для фильтра.
type EventsResult struct {
	Events []store.HostEvent `json:"events"`
	Total  int               `json:"total"`
	Unread int               `json:"unread"`
	Hosts  []string          `json:"hosts"`
	Hidden int               `json:"hidden"`
}

// QueryEvents — журнал с фильтрами. Ищется по всему журналу (он и так
// ограничен eventKeep), а не только по отданной странице.
func (m *Manager) QueryEvents(ctx context.Context, q EventQuery) (EventsResult, error) {
	all, err := m.db.ListHostEvents(ctx, eventKeep)
	if err != nil {
		return EventsResult{Events: []store.HostEvent{}}, err
	}
	if q.Limit <= 0 || q.Limit > eventKeep {
		q.Limit = 200
	}
	lang := msgs.FromContext(ctx)
	hide := m.EventSettings(ctx).Hide
	seen, _ := m.seenEventID(ctx)
	kinds := map[string]bool{}
	for _, k := range q.Kinds {
		if k != "" {
			kinds[k] = true
		}
	}
	text := strings.ToLower(strings.TrimSpace(q.Text))
	res := EventsResult{Events: []store.HostEvent{}, Hosts: []string{}}
	hostSet := map[string]bool{}
	for _, e := range all {
		if q.Allow != nil && !q.Allow(e.HostID) {
			continue
		}
		if !hostSet[e.HostName] {
			hostSet[e.HostName] = true
			res.Hosts = append(res.Hosts, e.HostName)
		}
		hidden := hide[e.Kind]
		if e.ID > seen && !hidden {
			res.Unread++
		}
		if hidden && !q.ShowHidden {
			res.Hidden++
			continue
		}
		if len(kinds) > 0 && !kinds[e.Kind] {
			continue
		}
		if q.Host != "" && e.HostName != q.Host {
			continue
		}
		if q.After > 0 && e.ID <= q.After {
			continue
		}
		e.Detail = msgs.Render(lang, e.DetailKey, e.DetailArgs, e.Detail)
		if text != "" && !strings.Contains(strings.ToLower(e.HostName+" "+e.HostAddr+" "+e.Detail), text) {
			continue
		}
		res.Total++
		if len(res.Events) < q.Limit {
			res.Events = append(res.Events, e)
		}
	}
	sort.Strings(res.Hosts)
	return res, nil
}

// MarkEventsSeen помечает журнал прочитанным до самого свежего события.
func (m *Manager) MarkEventsSeen(ctx context.Context) error {
	last, err := m.db.LastHostEventID(ctx)
	if err != nil {
		return err
	}
	return m.db.KVSet(ctx, seenEventsKey, fmt.Sprintf("%d", last))
}

// seenEventsKey — докуда журнал прочитан. Одно значение на хаб, а не на
// пользователя: оповещения общие, и «прочитано» здесь значит «кто-то из
// операторов это видел».
const seenEventsKey = "hub.events.seen"

func (m *Manager) seenEventID(ctx context.Context) (int64, error) {
	raw, ok, err := m.db.KVGet(ctx, seenEventsKey)
	if err != nil || !ok {
		return 0, err
	}
	var id int64
	if _, err := fmt.Sscanf(raw, "%d", &id); err != nil {
		return 0, nil
	}
	return id, nil
}

// EventSettings — какие оповещения записывать и о каких уведомлять.
//
// Одни на хаб, не на браузер: журнал общий, и «не записывать возвраты»
// должно действовать для всех, кто его смотрит. Всплывающие уведомления
// вкладка показывает по тем же настройкам.
type EventSettings struct {
	// Record — записывать событие в журнал; Notify — показывать
	// всплывающим. Отсутствующий вид считается включённым для записи и
	// выключенным для уведомления — так по умолчанию не теряется ничего,
	// а будят только тем, что требует действия.
	Record map[string]bool `json:"record"`
	Notify map[string]bool `json:"notify"`
	// Hide — записывать, но не показывать в журнале (пока не включено
	// «показать скрытые») и не считать в непрочитанных; всплывающих
	// уведомлений о скрытом виде не бывает.
	Hide map[string]bool `json:"hide"`
	// CollapseMinutes — «снова отвечает» не позже чем через столько минут
	// после «не отвечает» сворачивается с ним в одну строку «был
	// недоступен N мин»: моргнувшая сеть не должна оставлять две записи.
	// 0 — не сворачивать.
	CollapseMinutes int `json:"collapse_minutes"`
}

// EventKinds — все виды в порядке показа.
var EventKinds = []string{store.EventUnreachable, store.EventRecovered, store.EventProblems, store.EventResolved, store.EventJobFailed, store.EventRebooted, store.EventBans, store.EventForecast}

// defaultEventSettings — всё записывается; будят недоступностью,
// проблемами и провалом задания, но не возвратами.
func defaultEventSettings() EventSettings {
	s := EventSettings{Record: map[string]bool{}, Notify: map[string]bool{}, Hide: map[string]bool{}}
	for _, k := range EventKinds {
		s.Record[k] = true
	}
	s.Notify[store.EventUnreachable] = true
	s.Notify[store.EventProblems] = true
	s.Notify[store.EventJobFailed] = true
	s.Notify[store.EventRebooted] = true
	return s
}

const eventSettingsKey = "hub.events.settings"

// EventSettings читает настройки; чего нет в базе — берётся по умолчанию.
func (m *Manager) EventSettings(ctx context.Context) EventSettings {
	s := defaultEventSettings()
	raw, ok, err := m.db.KVGet(ctx, eventSettingsKey)
	if err != nil || !ok {
		return s
	}
	var saved EventSettings
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return s
	}
	for k, v := range saved.Record {
		s.Record[k] = v
	}
	for k, v := range saved.Notify {
		s.Notify[k] = v
	}
	for k, v := range saved.Hide {
		s.Hide[k] = v
		if v {
			s.Notify[k] = false
		}
	}
	s.CollapseMinutes = saved.CollapseMinutes
	return s
}

// SaveEventSettings записывает настройки.
func (m *Manager) SaveEventSettings(ctx context.Context, s EventSettings) error {
	if s.CollapseMinutes < 0 || s.CollapseMinutes > 1440 {
		return msgs.Errorf("hub.collapse01440Minutes")
	}
	// Скрытый вид не всплывает: уведомление о том, чего не видно в
	// журнале, только сбивает.
	for k, hidden := range s.Hide {
		if hidden && s.Notify != nil {
			s.Notify[k] = false
		}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return m.db.KVSet(ctx, eventSettingsKey, string(raw))
}
