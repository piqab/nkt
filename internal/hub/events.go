package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
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
func (m *Manager) recordEvent(ctx context.Context, host store.Host, kind, severity, detail string) {
	settings := m.EventSettings(ctx)
	if !settings.Record[kind] {
		return
	}
	if kind == store.EventRecovered && settings.CollapseMinutes > 0 && m.collapseOutage(ctx, host, settings.CollapseMinutes) {
		return
	}
	_, err := m.db.AddHostEvent(ctx, store.HostEvent{
		HostID: host.ID, HostName: host.Name, HostAddr: hostAddrLabel(ctx, host),
		Kind: kind, Severity: severity, Detail: detail,
	})
	if err != nil {
		m.log.Warn("не удалось записать оповещение", "host", host.Name, "kind", kind, "err", err)
		return
	}
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
	detail := msgs.Tc(ctx, "hub.unreachableMin", minutes)
	if minutes == 0 {
		detail = msgs.Tc(ctx, "hub.unreachableUnderMinute")
	}
	return m.db.RewriteHostEvent(ctx, last.ID, store.EventRecovered, detail) == nil
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
		m.recordEvent(ctx, host, store.EventRecovered, "", msgs.Tc(ctx, "hub.hostRespondsAgain"))
		return
	}
	m.recordEvent(ctx, host, store.EventUnreachable, "", reason)
}

// noteFindings записывает появление и исчезновение серьёзных находок.
//
// Считаются critical и high вместе: разделять их в оповещении незачем —
// смотреть всё равно идут в раздел находок, а строк в журнале станет
// вдвое больше.
func (m *Manager) noteFindings(ctx context.Context, hostID int64, findings map[string]int) {
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
		m.recordEvent(ctx, host, store.EventProblems, "critical+high",
			msgs.Tc(ctx, "hub.seriousFindingsNow", now, was))
	case now == 0:
		m.recordEvent(ctx, host, store.EventResolved, "critical+high",
			msgs.Tc(ctx, "hub.seriousFindingsLeft", was))
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

// Events отдаёт журнал и число непоказанных событий.
func (m *Manager) Events(ctx context.Context, limit int) ([]store.HostEvent, int, error) {
	events, err := m.db.ListHostEvents(ctx, limit)
	if err != nil {
		return nil, 0, err
	}
	seen, err := m.seenEventID(ctx)
	if err != nil {
		return events, 0, nil
	}
	unread, err := m.db.CountHostEventsAfter(ctx, seen)
	if err != nil {
		return events, 0, nil
	}
	return events, unread, nil
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
	// CollapseMinutes — «снова отвечает» не позже чем через столько минут
	// после «не отвечает» сворачивается с ним в одну строку «был
	// недоступен N мин»: моргнувшая сеть не должна оставлять две записи.
	// 0 — не сворачивать.
	CollapseMinutes int `json:"collapse_minutes"`
}

// EventKinds — все виды в порядке показа.
var EventKinds = []string{store.EventUnreachable, store.EventRecovered, store.EventProblems, store.EventResolved, store.EventJobFailed}

// defaultEventSettings — всё записывается; будят недоступностью,
// проблемами и провалом задания, но не возвратами.
func defaultEventSettings() EventSettings {
	s := EventSettings{Record: map[string]bool{}, Notify: map[string]bool{}}
	for _, k := range EventKinds {
		s.Record[k] = true
	}
	s.Notify[store.EventUnreachable] = true
	s.Notify[store.EventProblems] = true
	s.Notify[store.EventJobFailed] = true
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
	s.CollapseMinutes = saved.CollapseMinutes
	return s
}

// SaveEventSettings записывает настройки.
func (m *Manager) SaveEventSettings(ctx context.Context, s EventSettings) error {
	if s.CollapseMinutes < 0 || s.CollapseMinutes > 1440 {
		return msgs.Errorf("hub.collapse01440Minutes")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return m.db.KVSet(ctx, eventSettingsKey, string(raw))
}
