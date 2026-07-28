package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Target is something the prober checks on a schedule.
type Target struct {
	ID         int64  `json:"id"`
	Key        string `json:"key"`
	Label      string `json:"label"`
	Kind       string `json:"kind"` // http | https | tcp
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Path       string `json:"path"`
	HostHeader string `json:"host_header,omitempty"`
	Source     string `json:"source"` // nginx | haproxy | docker | manual
	Service    string `json:"service"`
	NodeID     string `json:"node_id"`
	Enabled    bool   `json:"enabled"`
	FirstSeen  string `json:"first_seen"`
	LastSeen   string `json:"last_seen"`
}

// Address renders host:port.
func (t Target) Address() string { return fmt.Sprintf("%s:%d", t.Host, t.Port) }

// UpsertTarget inserts a discovered target or refreshes its metadata.
// A target already pinned by an operator keeps its enabled flag.
func (d *DB) UpsertTarget(ctx context.Context, t Target) (int64, error) {
	now := Now()
	_, err := d.ExecContext(ctx,
		`INSERT INTO targets(key, label, kind, host, port, path, host_header, source, service, node_id, enabled, first_seen, last_seen)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		    label = excluded.label,
		    kind = excluded.kind,
		    host = excluded.host,
		    port = excluded.port,
		    path = excluded.path,
		    host_header = excluded.host_header,
		    source = excluded.source,
		    service = excluded.service,
		    node_id = excluded.node_id,
		    last_seen = excluded.last_seen`,
		t.Key, t.Label, t.Kind, t.Host, t.Port, t.Path, t.HostHeader, t.Source, t.Service, t.NodeID, now, now)
	if err != nil {
		return 0, err
	}
	var id int64
	err = d.QueryRowContext(ctx, `SELECT id FROM targets WHERE key = ?`, t.Key).Scan(&id)
	return id, err
}

const targetColumns = `id, key, label, kind, host, port, path, host_header, source, service, node_id, enabled, first_seen, last_seen`

func scanTarget(row interface{ Scan(...any) error }) (Target, error) {
	var t Target
	var enabled int
	err := row.Scan(&t.ID, &t.Key, &t.Label, &t.Kind, &t.Host, &t.Port, &t.Path, &t.HostHeader,
		&t.Source, &t.Service, &t.NodeID, &enabled, &t.FirstSeen, &t.LastSeen)
	t.Enabled = enabled != 0
	return t, err
}

// ListTargets returns every probe target, ordered by label.
func (d *DB) ListTargets(ctx context.Context, onlyEnabled bool) ([]Target, error) {
	q := `SELECT ` + targetColumns + ` FROM targets`
	if onlyEnabled {
		q += ` WHERE enabled = 1`
	}
	q += ` ORDER BY label, port`
	rows, err := d.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Target{}
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TargetByID fetches a single target.
func (d *DB) TargetByID(ctx context.Context, id int64) (Target, error) {
	t, err := scanTarget(d.QueryRowContext(ctx, `SELECT `+targetColumns+` FROM targets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	return t, err
}

// SetTargetEnabled pauses or resumes probing of a target.
func (d *DB) SetTargetEnabled(ctx context.Context, id int64, enabled bool) error {
	flag := 0
	if enabled {
		flag = 1
	}
	res, err := d.ExecContext(ctx, `UPDATE targets SET enabled = ? WHERE id = ?`, flag, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTarget removes a target and its history.
func (d *DB) DeleteTarget(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM targets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneDerivedTargets drops auto-discovered targets that the latest scan no
// longer sees. Manual targets are never touched.
func (d *DB) PruneDerivedTargets(ctx context.Context, seenBefore string) (int64, error) {
	res, err := d.ExecContext(ctx,
		`DELETE FROM targets WHERE source <> 'manual' AND last_seen < ?`, seenBefore)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// --------------------------------------------------------------------- probes

// ProbeResult is one availability check.
type ProbeResult struct {
	TargetID   int64   `json:"target_id"`
	TS         string  `json:"ts"`
	OK         bool    `json:"ok"`
	LatencyMS  float64 `json:"latency_ms"`
	StatusCode int     `json:"status_code,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// InsertProbeResults stores a batch of checks in one transaction.
func (d *DB) InsertProbeResults(ctx context.Context, results []ProbeResult) error {
	if len(results) == 0 {
		return nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO probe_results(target_id, ts, ok, latency_ms, status_code, error) VALUES(?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range results {
		ok := 0
		if r.OK {
			ok = 1
		}
		var code any
		if r.StatusCode > 0 {
			code = r.StatusCode
		}
		if _, err := stmt.ExecContext(ctx, r.TargetID, r.TS, ok, r.LatencyMS, code, r.Error); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TargetStatus is a target plus its current and recent health.
type TargetStatus struct {
	Target
	LastCheck    string  `json:"last_check,omitempty"`
	LastOK       *bool   `json:"last_ok,omitempty"`
	LastLatency  float64 `json:"last_latency_ms"`
	LastError    string  `json:"last_error,omitempty"`
	Checks24h    int     `json:"checks_24h"`
	Failures24h  int     `json:"failures_24h"`
	Uptime24h    float64 `json:"uptime_24h"`
	AvgLatency24 float64 `json:"avg_latency_24h"`
}

// TargetStatuses joins every target with its latest probe and a 24h rollup.
func (d *DB) TargetStatuses(ctx context.Context) ([]TargetStatus, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT t.id, t.key, t.label, t.kind, t.host, t.port, t.path, t.source, t.service,
		       t.node_id, t.enabled, t.first_seen, t.last_seen,
		       last.ts, last.ok, last.latency_ms, last.error,
		       COALESCE(agg.checks, 0), COALESCE(agg.failures, 0), COALESCE(agg.avg_latency, 0)
		  FROM targets t
		  LEFT JOIN (
		        SELECT p.target_id, p.ts, p.ok, p.latency_ms, p.error
		          FROM probe_results p
		          JOIN (SELECT target_id, MAX(ts) AS ts FROM probe_results GROUP BY target_id) m
		            ON m.target_id = p.target_id AND m.ts = p.ts
		       ) last ON last.target_id = t.id
		  LEFT JOIN (
		        SELECT target_id,
		               COUNT(*) AS checks,
		               SUM(CASE WHEN ok = 0 THEN 1 ELSE 0 END) AS failures,
		               AVG(latency_ms) AS avg_latency
		          FROM probe_results
		         WHERE ts >= datetime('now', '-1 day')
		         GROUP BY target_id
		       ) agg ON agg.target_id = t.id
		 ORDER BY t.label, t.port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []TargetStatus{}
	for rows.Next() {
		var s TargetStatus
		var enabled int
		var lastTS, lastErr sql.NullString
		var lastOK sql.NullInt64
		var lastLatency sql.NullFloat64
		if err := rows.Scan(&s.ID, &s.Key, &s.Label, &s.Kind, &s.Host, &s.Port, &s.Path,
			&s.Source, &s.Service, &s.NodeID, &enabled, &s.FirstSeen, &s.LastSeen,
			&lastTS, &lastOK, &lastLatency, &lastErr,
			&s.Checks24h, &s.Failures24h, &s.AvgLatency24); err != nil {
			return nil, err
		}
		s.Enabled = enabled != 0
		s.LastCheck = lastTS.String
		s.LastError = lastErr.String
		s.LastLatency = lastLatency.Float64
		if lastOK.Valid {
			ok := lastOK.Int64 != 0
			s.LastOK = &ok
		}
		if s.Checks24h > 0 {
			s.Uptime24h = float64(s.Checks24h-s.Failures24h) / float64(s.Checks24h) * 100
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Bucket is one aggregated slot of a time series.
type Bucket struct {
	Bucket     string  `json:"bucket"`
	Total      int     `json:"total"`
	OK         int     `json:"ok"`
	Uptime     float64 `json:"uptime"`
	AvgLatency float64 `json:"avg_latency_ms"`
	MaxLatency float64 `json:"max_latency_ms"`
}

// bucketExpr builds an SQLite strftime expression for the requested granularity,
// shifted by the caller's timezone offset so "по часам" means local hours.
func bucketExpr(column, granularity string, tzOffsetMinutes int) (string, error) {
	var format string
	switch granularity {
	case "hour", "":
		format = "%Y-%m-%dT%H:00"
	case "day":
		format = "%Y-%m-%d"
	case "minute":
		format = "%Y-%m-%dT%H:%M"
	default:
		return "", fmt.Errorf("unknown granularity %q", granularity)
	}
	return fmt.Sprintf("strftime('%s', %s, '%+d minutes')", format, column, tzOffsetMinutes), nil
}

// AvailabilityBuckets aggregates probe results into time slots.
func (d *DB) AvailabilityBuckets(ctx context.Context, targetID int64, since, granularity string, tzOffsetMinutes int) ([]Bucket, error) {
	expr, err := bucketExpr("ts", granularity, tzOffsetMinutes)
	if err != nil {
		return nil, err
	}
	args := []any{since}
	filter := ""
	if targetID > 0 {
		filter = " AND target_id = ?"
		args = append(args, targetID)
	}
	q := fmt.Sprintf(`
		SELECT %s AS bucket,
		       COUNT(*) AS total,
		       SUM(CASE WHEN ok = 1 THEN 1 ELSE 0 END) AS ok_count,
		       AVG(latency_ms), MAX(latency_ms)
		  FROM probe_results
		 WHERE ts >= ?%s
		 GROUP BY bucket
		 ORDER BY bucket`, expr, filter)

	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Bucket{}
	for rows.Next() {
		var b Bucket
		var avg, max sql.NullFloat64
		if err := rows.Scan(&b.Bucket, &b.Total, &b.OK, &avg, &max); err != nil {
			return nil, err
		}
		b.AvgLatency, b.MaxLatency = avg.Float64, max.Float64
		if b.Total > 0 {
			b.Uptime = float64(b.OK) / float64(b.Total) * 100
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// HeatCell is one day-of-week × hour-of-day slot.
type HeatCell struct {
	DOW    int     `json:"dow"` // 0 = Sunday, matching SQLite's %w
	Hour   int     `json:"hour"`
	Total  int     `json:"total"`
	OK     int     `json:"ok"`
	Uptime float64 `json:"uptime"`
	Value  float64 `json:"value"`
}

// AvailabilityHeatmap answers "when is this actually reachable?" — the weekly
// availability schedule, in the caller's timezone.
func (d *DB) AvailabilityHeatmap(ctx context.Context, targetID int64, since string, tzOffsetMinutes int) ([]HeatCell, error) {
	shift := fmt.Sprintf("'%+d minutes'", tzOffsetMinutes)
	args := []any{since}
	filter := ""
	if targetID > 0 {
		filter = " AND target_id = ?"
		args = append(args, targetID)
	}
	q := fmt.Sprintf(`
		SELECT CAST(strftime('%%w', ts, %s) AS INTEGER) AS dow,
		       CAST(strftime('%%H', ts, %s) AS INTEGER) AS hour,
		       COUNT(*),
		       SUM(CASE WHEN ok = 1 THEN 1 ELSE 0 END)
		  FROM probe_results
		 WHERE ts >= ?%s
		 GROUP BY dow, hour
		 ORDER BY dow, hour`, shift, shift, filter)

	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []HeatCell{}
	for rows.Next() {
		var c HeatCell
		if err := rows.Scan(&c.DOW, &c.Hour, &c.Total, &c.OK); err != nil {
			return nil, err
		}
		if c.Total > 0 {
			c.Uptime = float64(c.OK) / float64(c.Total) * 100
			c.Value = c.Uptime
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Outage is a contiguous stretch of failed checks.
type Outage struct {
	TargetID int64  `json:"target_id"`
	Label    string `json:"label"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Checks   int    `json:"checks"`
	Error    string `json:"error"`
}

// RecentOutages reconstructs downtime windows from raw probe rows. The volume
// per target is bounded by the retention window, so grouping in Go is cheaper
// and clearer than a windowed SQL query.
func (d *DB) RecentOutages(ctx context.Context, since string, limit int) ([]Outage, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT p.target_id, t.label, p.ts, p.ok, COALESCE(p.error, '')
		  FROM probe_results p JOIN targets t ON t.id = p.target_id
		 WHERE p.ts >= ?
		 ORDER BY p.target_id, p.ts`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Outage
	var cur *Outage
	var curTarget int64 = -1

	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for rows.Next() {
		var tid int64
		var label, ts, errText string
		var ok int
		if err := rows.Scan(&tid, &label, &ts, &ok, &errText); err != nil {
			return nil, err
		}
		if tid != curTarget {
			flush()
			curTarget = tid
		}
		if ok == 0 {
			if cur == nil {
				cur = &Outage{TargetID: tid, Label: label, Start: ts, End: ts, Checks: 1, Error: errText}
			} else {
				cur.End = ts
				cur.Checks++
				if cur.Error == "" {
					cur.Error = errText
				}
			}
		} else {
			flush()
		}
	}
	flush()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Newest first, capped.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []Outage{}
	}
	return out, nil
}

// --------------------------------------------------------------------- metrics

// MetricSample is one usage data point.
type MetricSample struct {
	TS      string  `json:"ts"`
	Source  string  `json:"source"`
	Subject string  `json:"subject"`
	Metric  string  `json:"metric"`
	Value   float64 `json:"value"`
}

// InsertMetrics stores a batch of usage samples.
func (d *DB) InsertMetrics(ctx context.Context, samples []MetricSample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO metric_samples(ts, source, subject, metric, value) VALUES(?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, s := range samples {
		if _, err := stmt.ExecContext(ctx, s.TS, s.Source, s.Subject, s.Metric, s.Value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MetricPoint is an aggregated usage slot for one subject.
type MetricPoint struct {
	Bucket  string  `json:"bucket"`
	Subject string  `json:"subject"`
	Value   float64 `json:"value"`
}

// MetricQuery narrows a usage series.
type MetricQuery struct {
	Source      string
	Metric      string
	Subjects    []string
	Since       string
	Granularity string
	TZOffset    int
	Aggregate   string // sum | avg | max
}

// MetricSeries aggregates usage samples into per-subject time buckets.
func (d *DB) MetricSeries(ctx context.Context, q MetricQuery) ([]MetricPoint, error) {
	expr, err := bucketExpr("ts", q.Granularity, q.TZOffset)
	if err != nil {
		return nil, err
	}
	agg := "SUM(value)"
	switch strings.ToLower(q.Aggregate) {
	case "", "sum":
	case "avg":
		agg = "AVG(value)"
	case "max":
		agg = "MAX(value)"
	default:
		return nil, fmt.Errorf("unknown aggregate %q", q.Aggregate)
	}

	where := []string{"ts >= ?"}
	args := []any{q.Since}
	if q.Source != "" {
		where = append(where, "source = ?")
		args = append(args, q.Source)
	}
	if q.Metric != "" {
		where = append(where, "metric = ?")
		args = append(args, q.Metric)
	}
	if len(q.Subjects) > 0 {
		where = append(where, "subject IN (?"+strings.Repeat(",?", len(q.Subjects)-1)+")")
		for _, s := range q.Subjects {
			args = append(args, s)
		}
	}

	sqlText := fmt.Sprintf(`
		SELECT %s AS bucket, subject, %s
		  FROM metric_samples
		 WHERE %s
		 GROUP BY bucket, subject
		 ORDER BY bucket, subject`, expr, agg, strings.Join(where, " AND "))

	rows, err := d.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []MetricPoint{}
	for rows.Next() {
		var p MetricPoint
		if err := rows.Scan(&p.Bucket, &p.Subject, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SubjectTotal is one row of a usage leaderboard.
type SubjectTotal struct {
	Subject string  `json:"subject"`
	Total   float64 `json:"total"`
	Samples int     `json:"samples"`
}

// MetricTop ranks subjects by total usage in the window.
func (d *DB) MetricTop(ctx context.Context, source, metric, since string, limit int) ([]SubjectTotal, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := d.QueryContext(ctx, `
		SELECT subject, SUM(value) AS total, COUNT(*)
		  FROM metric_samples
		 WHERE ts >= ? AND source = ? AND metric = ?
		 GROUP BY subject
		 ORDER BY total DESC
		 LIMIT ?`, since, source, metric, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SubjectTotal{}
	for rows.Next() {
		var s SubjectTotal
		if err := rows.Scan(&s.Subject, &s.Total, &s.Samples); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UsageHeatmap answers "when is this resource actually used?" — the weekly
// utilisation schedule.
func (d *DB) UsageHeatmap(ctx context.Context, source, metric, subject, since string, tzOffsetMinutes int) ([]HeatCell, error) {
	shift := fmt.Sprintf("'%+d minutes'", tzOffsetMinutes)
	where := []string{"ts >= ?", "source = ?", "metric = ?"}
	args := []any{since, source, metric}
	if subject != "" {
		where = append(where, "subject = ?")
		args = append(args, subject)
	}
	q := fmt.Sprintf(`
		SELECT CAST(strftime('%%w', ts, %s) AS INTEGER) AS dow,
		       CAST(strftime('%%H', ts, %s) AS INTEGER) AS hour,
		       COUNT(*), SUM(value)
		  FROM metric_samples
		 WHERE %s
		 GROUP BY dow, hour
		 ORDER BY dow, hour`, shift, shift, strings.Join(where, " AND "))

	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []HeatCell{}
	for rows.Next() {
		var c HeatCell
		var sum float64
		if err := rows.Scan(&c.DOW, &c.Hour, &c.Total, &sum); err != nil {
			return nil, err
		}
		c.Value = sum
		out = append(out, c)
	}
	return out, rows.Err()
}

// --------------------------------------------------------------------- counters

// CounterDelta converts a monotonic counter reading into the increment since the
// previous reading. The first reading of a counter yields ok=false, and a value
// that went backwards (service restart, counter reset) is treated the same way.
func (d *DB) CounterDelta(ctx context.Context, key string, value float64) (delta float64, ok bool, err error) {
	var prev float64
	err = d.QueryRowContext(ctx, `SELECT value FROM counter_state WHERE key = ?`, key).Scan(&prev)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		ok = false
	case err != nil:
		return 0, false, err
	default:
		if value >= prev {
			delta, ok = value-prev, true
		}
	}
	_, err = d.ExecContext(ctx,
		`INSERT INTO counter_state(key, value, ts) VALUES(?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, ts = excluded.ts`,
		key, value, Now())
	return delta, ok, err
}
