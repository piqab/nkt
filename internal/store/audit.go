package store

import (
	"context"
	"encoding/json"
	"strings"
)

// AuditEntry is one recorded state-changing operation.
type AuditEntry struct {
	ID       int64  `json:"id"`
	TS       string `json:"ts"`
	Username string `json:"username"`
	Action   string `json:"action"`
	Target   string `json:"target"`
	Result   string `json:"result"`
	Detail   string `json:"detail"`
}

// Audit records an operation. Non-string details are JSON encoded.
func (d *DB) Audit(ctx context.Context, username, action, target, result string, detail any) {
	var text string
	switch v := detail.(type) {
	case nil:
	case string:
		text = v
	case error:
		text = v.Error()
	default:
		if raw, err := json.Marshal(v); err == nil {
			text = string(raw)
		}
	}
	// Auditing must never break the operation it describes, so errors are swallowed
	// deliberately; a failed insert is visible as a gap in the log.
	_, _ = d.ExecContext(ctx,
		`INSERT INTO audit_log(ts, username, action, target, result, detail) VALUES(?, ?, ?, ?, ?, ?)`,
		Now(), username, action, target, result, text)
}

// AuditFilter narrows an audit query.
type AuditFilter struct {
	Username string
	Action   string
	Result   string
	Since    string
	Limit    int
	Offset   int
}

// AuditKinds — виды действий журнала: префиксы до первой точки
// («clamav», «firewall», «system»), что реально в нём встречаются, по
// алфавиту. Фильтр «Журнала действий» строится из них, а не из списка в
// интерфейсе, который отставал от новых разделов.
func (d *DB) AuditKinds(ctx context.Context) ([]string, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT DISTINCT CASE WHEN instr(action, '.') > 0 THEN substr(action, 1, instr(action, '.') - 1) ELSE action END AS kind
		FROM audit_log WHERE action <> '' ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ListAudit returns audit entries, newest first.
func (d *DB) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	var where []string
	var args []any
	if f.Username != "" {
		where = append(where, "username = ?")
		args = append(args, f.Username)
	}
	if f.Action != "" {
		where = append(where, "action LIKE ?")
		args = append(args, f.Action+"%")
	}
	if f.Result != "" {
		where = append(where, "result = ?")
		args = append(args, f.Result)
	}
	if f.Since != "" {
		where = append(where, "ts >= ?")
		args = append(args, f.Since)
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}

	q := `SELECT id, ts, username, action, target, result, detail FROM audit_log`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY id DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)

	rows, err := d.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Username, &e.Action, &e.Target, &e.Result, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
