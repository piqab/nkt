package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Site — сайт хаба: домен(ы) на хосте за прокси, с сертификатом.
type Site struct {
	ID            int64    `json:"id"`
	Domains       []string `json:"domains"`
	HostID        int64    `json:"host_id"`
	Proxy         string   `json:"proxy"`
	Stack         string   `json:"stack,omitempty"`
	Service       string   `json:"service,omitempty"`
	ContainerPort int      `json:"container_port,omitempty"`
	Upstream      string   `json:"upstream,omitempty"`
	OpenFirewall  bool     `json:"open_firewall"`
	// PipelineID — конвейер, чей блок site: этот сайт описывает (0 — сайт
	// заведён вручную в «Сайтах»).
	PipelineID int64  `json:"pipeline_id,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	// Check — последняя проверка снаружи (JSON hub.SiteCheck).
	Check     json.RawMessage `json:"check,omitempty"`
	JobID     int64           `json:"job_id,omitempty"`
	Author    string          `json:"author,omitempty"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// Состояния сайта.
const (
	SiteSettingUp = "setting-up"
	SiteOK        = "ok"
	SiteFailed    = "failed"
)

const siteColumns = `id, domains, host_id, proxy, stack, service, container_port, upstream, open_firewall, pipeline_id, status, error, check_json, job_id, author, created_at, updated_at`

func scanSite(row interface{ Scan(...any) error }) (Site, error) {
	var s Site
	var domains, check string
	err := row.Scan(&s.ID, &domains, &s.HostID, &s.Proxy, &s.Stack, &s.Service, &s.ContainerPort, &s.Upstream,
		&s.OpenFirewall, &s.PipelineID, &s.Status, &s.Error, &check, &s.JobID, &s.Author, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	_ = json.Unmarshal([]byte(domains), &s.Domains)
	if check != "" {
		s.Check = json.RawMessage(check)
	}
	return s, nil
}

// SaveSite заводит сайт (ID 0) или меняет его настройки.
func (d *DB) SaveSite(ctx context.Context, s Site) (int64, error) {
	domains, _ := json.Marshal(s.Domains)
	now := Now()
	if s.ID == 0 {
		res, err := d.ExecContext(ctx, `INSERT INTO sites(domains, host_id, proxy, stack, service, container_port, upstream, open_firewall, pipeline_id, author, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			string(domains), s.HostID, s.Proxy, s.Stack, s.Service, s.ContainerPort, s.Upstream, s.OpenFirewall, s.PipelineID, s.Author, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := d.ExecContext(ctx, `UPDATE sites SET domains = ?, host_id = ?, proxy = ?, stack = ?, service = ?, container_port = ?,
		upstream = ?, open_firewall = ?, pipeline_id = ?, author = ?, updated_at = ? WHERE id = ?`,
		string(domains), s.HostID, s.Proxy, s.Stack, s.Service, s.ContainerPort, s.Upstream, s.OpenFirewall, s.PipelineID, s.Author, now, s.ID)
	return s.ID, err
}

// SetSiteState — состояние, ошибка и задание сайта.
func (d *DB) SetSiteState(ctx context.Context, id int64, status, errText string, jobID int64) error {
	_, err := d.ExecContext(ctx, `UPDATE sites SET status = ?, error = ?, job_id = CASE WHEN ? > 0 THEN ? ELSE job_id END, updated_at = ? WHERE id = ?`,
		status, errText, jobID, jobID, Now(), id)
	return err
}

// SetSiteCheck — последняя проверка снаружи.
func (d *DB) SetSiteCheck(ctx context.Context, id int64, check []byte) error {
	_, err := d.ExecContext(ctx, `UPDATE sites SET check_json = ? WHERE id = ?`, string(check), id)
	return err
}

// SiteByID — сайт.
func (d *DB) SiteByID(ctx context.Context, id int64) (Site, error) {
	s, err := scanSite(d.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, ErrNotFound
	}
	return s, err
}

// ListSites — все сайты.
func (d *DB) ListSites(ctx context.Context) ([]Site, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+siteColumns+` FROM sites ORDER BY domains`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Site{}
	for rows.Next() {
		s, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSite — забыть сайт.
func (d *DB) DeleteSite(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM sites WHERE id = ?`, id)
	return err
}
