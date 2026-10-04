package store

import (
	"context"
	"strings"
)

// Почасовые сводки рядов и проверок доступности — для «Мониторинга» хаба:
// хост отдаёт их сжатыми вместо минутных рядов.

// HourlyMetric — час одного ряда.
type HourlyMetric struct {
	Source  string
	Subject string
	Metric  string
	// Hour — «2026-10-04T05» (UTC).
	Hour string
	Avg  float64
	Max  float64
	Sum  float64
	N    int
}

// MetricHourly — почасовые сводки рядов источников sources за
// [since, until) (RFC 3339).
func (d *DB) MetricHourly(ctx context.Context, since, until string, sources []string) ([]HourlyMetric, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	args := []any{since, until}
	ph := make([]string, len(sources))
	for i, s := range sources {
		ph[i] = "?"
		args = append(args, s)
	}
	args[0], args[1] = hourOf(since), hourOf(until)
	rows, err := d.reader().QueryContext(ctx, `SELECT source, subject, metric, hour,
		sum / n, max, sum, n
		FROM metric_hourly WHERE hour >= ? AND hour < ? AND n > 0 AND source IN (`+strings.Join(ph, ",")+`)
		ORDER BY source, subject, metric, hour`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourlyMetric
	for rows.Next() {
		var m HourlyMetric
		if err := rows.Scan(&m.Source, &m.Subject, &m.Metric, &m.Hour, &m.Avg, &m.Max, &m.Sum, &m.N); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// HourlyProbe — час проверок одной цели.
type HourlyProbe struct {
	TargetID int64
	Hour     string
	OK       int
	Total    int
	// LatencyMS — средняя задержка удачных проверок.
	LatencyMS float64
}

// ProbeHourly — почасовые итоги проверок всех целей за [since, until).
func (d *DB) ProbeHourly(ctx context.Context, since, until string) ([]HourlyProbe, error) {
	rows, err := d.reader().QueryContext(ctx, `SELECT target_id, substr(ts, 1, 13) AS h, SUM(ok), COUNT(*),
		COALESCE(AVG(CASE WHEN ok THEN latency_ms END), 0)
		FROM probe_results WHERE ts >= ? AND ts < ?
		GROUP BY target_id, h ORDER BY target_id, h`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourlyProbe
	for rows.Next() {
		var p HourlyProbe
		if err := rows.Scan(&p.TargetID, &p.Hour, &p.OK, &p.Total, &p.LatencyMS); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
