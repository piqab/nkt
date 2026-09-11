package store

import (
	"context"
	"time"
)

// Оповещения хаба — то, что случилось с хостами, пока никто не смотрел:
// хост перестал отвечать, появились новые проблемы, задание провалилось.
//
// Всплывающее уведомление браузера живёт, пока открыта вкладка, и никуда
// не записывается: закрыл — и не узнал. Поэтому события рождаются на
// стороне хаба, в фоновом опросе, и складываются сюда; браузер их только
// показывает.

// Виды событий.
const (
	// EventUnreachable — хост перестал отвечать на опрос.
	EventUnreachable = "unreachable"
	// EventRecovered — снова отвечает.
	EventRecovered = "recovered"
	// EventProblems — выросло число находок critical/high.
	EventProblems = "problems"
	// EventResolved — находок critical/high больше нет.
	EventResolved = "resolved"
	// EventJobFailed — фоновое задание хаба завершилось ошибкой.
	EventJobFailed = "job-failed"
)

// HostEvent — одно оповещение.
type HostEvent struct {
	ID int64  `json:"id"`
	TS string `json:"ts"`
	// HostID — для ссылки на хост; ноль у событий, не привязанных к нему.
	HostID int64 `json:"host_id,omitempty"`
	// HostName и HostAddr записаны на момент события: хост переименуют или
	// удалят, а журнал должен остаться читаемым.
	HostName string `json:"host_name"`
	HostAddr string `json:"host_addr"`
	Kind     string `json:"kind"`
	Severity string `json:"severity,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// AddHostEvent записывает оповещение.
func (d *DB) AddHostEvent(ctx context.Context, e HostEvent) (int64, error) {
	if e.TS == "" {
		e.TS = FormatTime(time.Now())
	}
	res, err := d.ExecContext(ctx, `
		INSERT INTO host_events (ts, host_id, host_name, host_addr, kind, severity, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.TS, e.HostID, e.HostName, e.HostAddr, e.Kind, e.Severity, e.Detail)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListHostEvents отдаёт последние оповещения, новые первыми.
func (d *DB) ListHostEvents(ctx context.Context, limit int) ([]HostEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := d.QueryContext(ctx, `
		SELECT id, ts, host_id, host_name, host_addr, kind, severity, detail
		FROM host_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Пустой срез, а не nil: nil уезжает в JSON как null и роняет список
	// в браузере.
	out := []HostEvent{}
	for rows.Next() {
		var e HostEvent
		if err := rows.Scan(&e.ID, &e.TS, &e.HostID, &e.HostName, &e.HostAddr,
			&e.Kind, &e.Severity, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountHostEventsAfter считает события новее данного — столько их и не
// показано оператору.
func (d *DB) CountHostEventsAfter(ctx context.Context, afterID int64) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM host_events WHERE id > ?`, afterID).Scan(&n)
	return n, err
}

// LastHostEventID — номер самого свежего события.
func (d *DB) LastHostEventID(ctx context.Context) (int64, error) {
	var id int64
	err := d.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM host_events`).Scan(&id)
	return id, err
}

// PruneHostEvents оставляет последние keep событий.
//
// Журнал не архив: смысл в том, что случилось за последние дни, а не за
// всё время. Без чистки таблица растёт линейно от числа хостов и частоты
// опроса.
func (d *DB) PruneHostEvents(ctx context.Context, keep int) error {
	if keep <= 0 {
		keep = 2000
	}
	_, err := d.ExecContext(ctx, `
		DELETE FROM host_events WHERE id <= (
			SELECT COALESCE(MIN(id), 0) FROM (
				SELECT id FROM host_events ORDER BY id DESC LIMIT ?
			)
		) - 1`, keep)
	return err
}
