package store

import (
	"context"
	"database/sql"
	"strings"
)

// История «Мониторинга» на хабе: почасовые и суточные сводки рядов
// каждого хоста (CPU, память, диски, каждый контейнер и машина) и
// проверок его целей доступности. Хаб забирает их с хостов раз в час и
// хранит дольше, чем хосты (см. hub/monitoring.go).
//
// Проверки целей лежат рядами source "probe": subject — ключ цели,
// метрики ok (sum — удачных), total (sum — всего), latency_ms (avg).

const monSchema = `
CREATE TABLE IF NOT EXISTS mon_hourly (
    host_id INTEGER NOT NULL,
    source  TEXT NOT NULL,
    subject TEXT NOT NULL,
    metric  TEXT NOT NULL,
    hour    TEXT NOT NULL,            -- 2026-10-04T05 (UTC)
    avg     REAL NOT NULL,
    max     REAL NOT NULL,
    sum     REAL NOT NULL,
    PRIMARY KEY (host_id, source, subject, metric, hour)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_mon_hourly_hour ON mon_hourly(hour);
CREATE TABLE IF NOT EXISTS mon_daily (
    host_id INTEGER NOT NULL,
    source  TEXT NOT NULL,
    subject TEXT NOT NULL,
    metric  TEXT NOT NULL,
    day     TEXT NOT NULL,            -- 2026-10-04 (UTC)
    avg     REAL NOT NULL,
    max     REAL NOT NULL,
    sum     REAL NOT NULL,
    PRIMARY KEY (host_id, source, subject, metric, day)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_mon_daily_day ON mon_daily(day);
CREATE TABLE IF NOT EXISTS mon_targets (
    host_id INTEGER NOT NULL,
    key     TEXT NOT NULL,
    label   TEXT NOT NULL,
    kind    TEXT NOT NULL,
    host    TEXT NOT NULL,
    port    INTEGER NOT NULL,
    service TEXT NOT NULL,
    enabled INTEGER NOT NULL,
    PRIMARY KEY (host_id, key)
) WITHOUT ROWID;
`

// MonRow — час (или день) одного ряда.
type MonRow struct {
	Source  string  `json:"s"`
	Subject string  `json:"j"`
	Metric  string  `json:"m"`
	At      string  `json:"t"`
	Avg     float64 `json:"a"`
	Max     float64 `json:"x"`
	Sum     float64 `json:"u"`
}

// MonTarget — цель доступности хоста (подписи для истории проверок).
type MonTarget struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Service string `json:"service"`
	Enabled bool   `json:"enabled"`
}

// MonUpsertHourly — записать часы хоста (повтор того же часа заменяет).
// insertOnly — не трогать уже имеющиеся часы (дополнение при импорте).
func (d *DB) MonUpsertHourly(ctx context.Context, hostID int64, rows []MonRow, insertOnly bool) error {
	return d.monUpsert(ctx, "mon_hourly", "hour", hostID, rows, insertOnly)
}

// MonUpsertDaily — то же для дней.
func (d *DB) MonUpsertDaily(ctx context.Context, hostID int64, rows []MonRow, insertOnly bool) error {
	return d.monUpsert(ctx, "mon_daily", "day", hostID, rows, insertOnly)
}

func (d *DB) monUpsert(ctx context.Context, table, col string, hostID int64, rows []MonRow, insertOnly bool) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	verb := "INSERT OR REPLACE"
	if insertOnly {
		verb = "INSERT OR IGNORE"
	}
	stmt, err := tx.PrepareContext(ctx, verb+` INTO `+table+`(host_id, source, subject, metric, `+col+`, avg, max, sum) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, hostID, r.Source, r.Subject, r.Metric, r.At, r.Avg, r.Max, r.Sum); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MonSetTargets — цели хоста (заменяют прежний список).
func (d *DB) MonSetTargets(ctx context.Context, hostID int64, list []MonTarget) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM mon_targets WHERE host_id = ?`, hostID); err != nil {
		return err
	}
	for _, t := range list {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO mon_targets(host_id, key, label, kind, host, port, service, enabled) VALUES (?,?,?,?,?,?,?,?)`,
			hostID, t.Key, t.Label, t.Kind, t.Host, t.Port, t.Service, t.Enabled); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MonTargets — цели хоста.
func (d *DB) MonTargets(ctx context.Context, hostID int64) ([]MonTarget, error) {
	rows, err := d.QueryContext(ctx, `SELECT key, label, kind, host, port, service, enabled FROM mon_targets WHERE host_id = ? ORDER BY label`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonTarget
	for rows.Next() {
		var t MonTarget
		if err := rows.Scan(&t.Key, &t.Label, &t.Kind, &t.Host, &t.Port, &t.Service, &t.Enabled); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MonLastHour — последний сохранённый час хоста ("" — истории нет).
func (d *DB) MonLastHour(ctx context.Context, hostID int64) (string, error) {
	var h sql.NullString
	err := d.QueryRowContext(ctx, `SELECT MAX(hour) FROM mon_hourly WHERE host_id = ?`, hostID).Scan(&h)
	return h.String, err
}

// MonHasHistory — есть ли у хоста хоть что-то.
func (d *DB) MonHasHistory(ctx context.Context, hostID int64) bool {
	h, err := d.MonLastHour(ctx, hostID)
	if err == nil && h != "" {
		return true
	}
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM mon_daily WHERE host_id = ? LIMIT 1`, hostID).Scan(&n)
	return n > 0
}

// MonRollupDaily — суточные сводки дней days из часовых.
func (d *DB) MonRollupDaily(ctx context.Context, hostID int64, days []string) error {
	for _, day := range days {
		if _, err := d.ExecContext(ctx, `INSERT OR REPLACE INTO mon_daily(host_id, source, subject, metric, day, avg, max, sum)
			SELECT host_id, source, subject, metric, substr(hour, 1, 10), AVG(avg), MAX(max), SUM(sum)
			FROM mon_hourly WHERE host_id = ? AND hour >= ? AND hour < ?
			GROUP BY host_id, source, subject, metric`, hostID, day+"T00", day+"T99"); err != nil {
			return err
		}
	}
	return nil
}

// MonPurge — часы старше hourBefore, дни старше dayBefore и история
// хостов, которых больше нет.
func (d *DB) MonPurge(ctx context.Context, hourBefore, dayBefore string) error {
	for _, q := range []struct {
		sql string
		arg string
	}{
		{`DELETE FROM mon_hourly WHERE hour < ?`, hourBefore},
		{`DELETE FROM mon_daily WHERE day < ?`, dayBefore},
	} {
		if _, err := d.ExecContext(ctx, q.sql, q.arg); err != nil {
			return err
		}
	}
	for _, t := range []string{"mon_hourly", "mon_daily", "mon_targets"} {
		if _, err := d.ExecContext(ctx, `DELETE FROM `+t+` WHERE host_id NOT IN (SELECT id FROM hosts) AND host_id != -1`); err != nil {
			return err
		}
	}
	return nil
}

// MonSeriesQuery — выбор рядов.
type MonSeriesQuery struct {
	HostID  int64
	Source  string
	Subject string // пусто — все
	Metric  string // пусто — все
	From    string // час или день (включительно)
	To      string // исключительно
	Daily   bool
}

// MonSeries — строки рядов по выбору, по времени.
func (d *DB) MonSeries(ctx context.Context, q MonSeriesQuery) ([]MonRow, error) {
	table, col := "mon_hourly", "hour"
	if q.Daily {
		table, col = "mon_daily", "day"
	}
	where := []string{"host_id = ?", col + " >= ?", col + " < ?"}
	args := []any{q.HostID, q.From, q.To}
	if q.Source != "" {
		where = append(where, "source = ?")
		args = append(args, q.Source)
	}
	if q.Subject != "" {
		where = append(where, "subject = ?")
		args = append(args, q.Subject)
	}
	if q.Metric != "" {
		where = append(where, "metric = ?")
		args = append(args, q.Metric)
	}
	rows, err := d.QueryContext(ctx, `SELECT source, subject, metric, `+col+`, avg, max, sum FROM `+table+
		` WHERE `+strings.Join(where, " AND ")+` ORDER BY source, subject, metric, `+col, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonRow
	for rows.Next() {
		var r MonRow
		if err := rows.Scan(&r.Source, &r.Subject, &r.Metric, &r.At, &r.Avg, &r.Max, &r.Sum); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MonAllHourly — все часы хоста за [from, to) (для экспорта).
func (d *DB) MonAllHourly(ctx context.Context, hostID int64, from, to string) ([]MonRow, error) {
	return d.MonSeries(ctx, MonSeriesQuery{HostID: hostID, From: from, To: to})
}

// MonAllDaily — все дни хоста (для экспорта).
func (d *DB) MonAllDaily(ctx context.Context, hostID int64) ([]MonRow, error) {
	return d.MonSeries(ctx, MonSeriesQuery{HostID: hostID, From: "0000", To: "9999", Daily: true})
}

// MonAgg — ряд хоста за период: среднее средних, пик, сумма, сколько
// часов (дней) есть.
type MonAgg struct {
	HostID  int64
	Source  string
	Subject string
	Metric  string
	Avg     float64
	Max     float64
	Sum     float64
	N       int
	// Last — значение (среднее) в последнем часе (дне) периода.
	Last   float64
	LastAt string
}

// MonAggregate — агрегаты всех рядов всех хостов за [from, to).
func (d *DB) MonAggregate(ctx context.Context, from, to string, daily bool) ([]MonAgg, error) {
	table, col := "mon_hourly", "hour"
	if daily {
		table, col = "mon_daily", "day"
	}
	rows, err := d.QueryContext(ctx, `SELECT a.host_id, a.source, a.subject, a.metric, AVG(a.avg), MAX(a.max), SUM(a.sum), COUNT(*),
		(SELECT b.avg FROM `+table+` b WHERE b.host_id = a.host_id AND b.source = a.source AND b.subject = a.subject AND b.metric = a.metric
			AND b.`+col+` >= ? AND b.`+col+` < ? ORDER BY b.`+col+` DESC LIMIT 1),
		MAX(a.`+col+`)
		FROM `+table+` a WHERE a.`+col+` >= ? AND a.`+col+` < ?
		GROUP BY a.host_id, a.source, a.subject, a.metric`, from, to, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonAgg
	for rows.Next() {
		var a MonAgg
		if err := rows.Scan(&a.HostID, &a.Source, &a.Subject, &a.Metric, &a.Avg, &a.Max, &a.Sum, &a.N, &a.Last, &a.LastAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MonHourCell — час (UTC) по всем хостам: сумма и среднее метрики.
type MonHourCell struct {
	Hour   string
	Metric string
	Sum    float64
	Avg    float64
}

// MonByHour — по часам за [from, to) для источника (все хосты вместе).
func (d *DB) MonByHour(ctx context.Context, from, to, source string) ([]MonHourCell, error) {
	rows, err := d.QueryContext(ctx, `SELECT hour, metric, SUM(sum), AVG(avg) FROM mon_hourly
		WHERE hour >= ? AND hour < ? AND source = ? GROUP BY hour, metric`, from, to, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonHourCell
	for rows.Next() {
		var c MonHourCell
		if err := rows.Scan(&c.Hour, &c.Metric, &c.Sum, &c.Avg); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
