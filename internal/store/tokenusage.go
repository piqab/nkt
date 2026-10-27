package store

import (
	"context"
	"time"
)

// Учёт занятого токеном «по правам»: что выложено и создано через него и
// ещё существует. Суммы идут в потолки токена; запись заменяется
// повторной выкладкой того же (тот же kind, хост и ref), снимается
// удалением, сверкой с хостами и вручную («освободить»).

// Виды записей учёта.
const (
	UsageCompose = "compose" // стек compose на хосте: ref — имя стека
	UsageVM      = "vm"      // машина: host_id — хост, ref — имя машины
	UsageK8sNS   = "k8s"     // namespace: host_id — id кластера, ref — namespace
	// UsageSnapshot — снимок машины: host_id — хост, ref — «машина/снимок»
	// (LXD — «lxd/экземпляр/снимок»); диск — размер диска машины (худший
	// случай: снимок дорастает до него).
	UsageSnapshot = "snapshot"
)

// TokenUsage — одна запись учёта.
type TokenUsage struct {
	TokenID   int64   `json:"token_id"`
	Kind      string  `json:"kind"`
	HostID    int64   `json:"host_id"`
	Ref       string  `json:"ref"`
	Label     string  `json:"label,omitempty"`
	CPU       float64 `json:"cpu"`
	MemMB     int64   `json:"mem_mb"`
	DiskGB    int64   `json:"disk_gb"`
	UpdatedAt string  `json:"updated_at"`
}

// SetTokenUsage добавляет или заменяет запись.
func (d *DB) SetTokenUsage(ctx context.Context, u TokenUsage) error {
	_, err := d.ExecContext(ctx, `INSERT INTO token_usage(token_id, kind, host_id, ref, label, cpu, mem_mb, disk_gb, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(token_id, kind, host_id, ref) DO UPDATE SET label = excluded.label, cpu = excluded.cpu, mem_mb = excluded.mem_mb,
			disk_gb = excluded.disk_gb, updated_at = excluded.updated_at`,
		u.TokenID, u.Kind, u.HostID, u.Ref, u.Label, u.CPU, u.MemMB, u.DiskGB, FormatTime(time.Now()))
	return err
}

// DeleteTokenUsage снимает запись (у всех токенов, если tokenID == 0:
// стек удалён — неважно, кем выложен).
func (d *DB) DeleteTokenUsage(ctx context.Context, tokenID int64, kind string, hostID int64, ref string) error {
	if tokenID == 0 {
		_, err := d.ExecContext(ctx, `DELETE FROM token_usage WHERE kind = ? AND host_id = ? AND ref = ?`, kind, hostID, ref)
		return err
	}
	_, err := d.ExecContext(ctx, `DELETE FROM token_usage WHERE token_id = ? AND kind = ? AND host_id = ? AND ref = ?`, tokenID, kind, hostID, ref)
	return err
}

// DeleteTokenUsagePrefix убирает записи вида kind на хосте, чей ref
// начинается с prefix (снимки удалённой машины).
func (d *DB) DeleteTokenUsagePrefix(ctx context.Context, kind string, hostID int64, prefix string) error {
	_, err := d.ExecContext(ctx, `DELETE FROM token_usage WHERE kind = ? AND host_id = ? AND substr(ref, 1, ?) = ?`, kind, hostID, len(prefix), prefix)
	return err
}

// ListTokenUsage — записи токена (tokenID == 0 — все).
func (d *DB) ListTokenUsage(ctx context.Context, tokenID int64) ([]TokenUsage, error) {
	q := `SELECT token_id, kind, host_id, ref, label, cpu, mem_mb, disk_gb, updated_at FROM token_usage`
	var args []any
	if tokenID != 0 {
		q += ` WHERE token_id = ?`
		args = append(args, tokenID)
	}
	rows, err := d.QueryContext(ctx, q+` ORDER BY kind, host_id, ref`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TokenUsage{}
	for rows.Next() {
		var u TokenUsage
		if err := rows.Scan(&u.TokenID, &u.Kind, &u.HostID, &u.Ref, &u.Label, &u.CPU, &u.MemMB, &u.DiskGB, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
