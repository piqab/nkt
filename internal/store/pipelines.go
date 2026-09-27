package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Pipeline — конвейер выкладки хаба (описание — в internal/deploy).
type Pipeline struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Content      string `json:"content,omitempty"`
	HookID       string `json:"hook_id"`
	HookSecret   []byte `json:"-"`
	GitCred      []byte `json:"-"`
	RegistryCred []byte `json:"-"`
	Enabled      bool   `json:"enabled"`
	LastCommit   string `json:"last_commit,omitempty"`
	LastTag      string `json:"last_tag,omitempty"`
	Author       string `json:"author,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	// HasGitCred / HasRegistryCred — для интерфейса: задан ли доступ.
	HasGitCred      bool `json:"has_git_cred"`
	HasRegistryCred bool `json:"has_registry_cred"`
}

// PipelineVersion — прошлая редакция описания.
type PipelineVersion struct {
	ID         int64  `json:"id"`
	PipelineID int64  `json:"pipeline_id"`
	TS         string `json:"ts"`
	Author     string `json:"author,omitempty"`
	Note       string `json:"note,omitempty"`
	Content    string `json:"content,omitempty"`
}

// Deployment — одна выкладка.
type Deployment struct {
	ID         int64  `json:"id"`
	PipelineID int64  `json:"pipeline_id"`
	Ref        string `json:"ref,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Tag        string `json:"tag,omitempty"`
	Trigger    string `json:"trigger"`
	Author     string `json:"author,omitempty"`
	JobID      int64  `json:"job_id,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	CreatedAt  string `json:"created_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// Состояния выкладки.
const (
	DeployQueued    = "queued"
	DeployRunning   = "running"
	DeploySucceeded = "succeeded"
	DeployFailed    = "failed"
)

const pipelineColumns = `id, name, content, hook_id, hook_secret, git_cred, registry_cred, enabled, last_commit, last_tag, author, created_at, updated_at`

func scanPipeline(row interface{ Scan(...any) error }) (Pipeline, error) {
	var p Pipeline
	err := row.Scan(&p.ID, &p.Name, &p.Content, &p.HookID, &p.HookSecret, &p.GitCred, &p.RegistryCred, &p.Enabled,
		&p.LastCommit, &p.LastTag, &p.Author, &p.CreatedAt, &p.UpdatedAt)
	p.HasGitCred, p.HasRegistryCred = len(p.GitCred) > 0, len(p.RegistryCred) > 0
	return p, err
}

// CreatePipeline заводит конвейер с первой редакцией.
func (db *DB) CreatePipeline(ctx context.Context, p Pipeline) (int64, error) {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `INSERT INTO pipelines (name, content, hook_id, hook_secret, enabled, author, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?)`, p.Name, p.Content, p.HookID, p.HookSecret, p.Author, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_versions (pipeline_id, ts, author, note, content) VALUES (?, ?, ?, ?, ?)`,
		id, now, p.Author, "", p.Content); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdatePipelineContent меняет описание, сохраняя редакцию.
func (db *DB) UpdatePipelineContent(ctx context.Context, id int64, content, author, note string) error {
	now := FormatTime(time.Now())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE pipelines SET content = ?, author = ?, updated_at = ? WHERE id = ?`, content, author, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_versions (pipeline_id, ts, author, note, content) VALUES (?, ?, ?, ?, ?)`,
		id, now, author, note, content); err != nil {
		return err
	}
	return tx.Commit()
}

// SetPipelineSecrets меняет зашифрованные секреты (nil — не трогать).
func (db *DB) SetPipelineSecrets(ctx context.Context, id int64, hookSecret, gitCred, registryCred []byte) error {
	for _, c := range []struct {
		col string
		v   []byte
	}{{"hook_secret", hookSecret}, {"git_cred", gitCred}, {"registry_cred", registryCred}} {
		if c.v == nil {
			continue
		}
		v := any(c.v)
		if len(c.v) == 0 {
			v = nil
		}
		if _, err := db.ExecContext(ctx, `UPDATE pipelines SET `+c.col+` = ? WHERE id = ?`, v, id); err != nil {
			return err
		}
	}
	return nil
}

// SetPipelineEnabled включает и выключает конвейер.
func (db *DB) SetPipelineEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := db.ExecContext(ctx, `UPDATE pipelines SET enabled = ? WHERE id = ?`, enabled, id)
	return err
}

// SetPipelineDeployed запоминает последнее выложенное.
func (db *DB) SetPipelineDeployed(ctx context.Context, id int64, commit, tag string) error {
	_, err := db.ExecContext(ctx, `UPDATE pipelines SET last_commit = ?, last_tag = ? WHERE id = ?`, commit, tag, id)
	return err
}

// DeletePipeline убирает конвейер с историей и выкладками.
func (db *DB) DeletePipeline(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM pipelines WHERE id = ?`, id)
	return err
}

// PipelineByID — конвейер целиком.
func (db *DB) PipelineByID(ctx context.Context, id int64) (Pipeline, error) {
	p, err := scanPipeline(db.QueryRowContext(ctx, `SELECT `+pipelineColumns+` FROM pipelines WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Pipeline{}, ErrNotFound
	}
	return p, err
}

// PipelineByHook — конвейер по адресу вебхука.
func (db *DB) PipelineByHook(ctx context.Context, hookID string) (Pipeline, error) {
	p, err := scanPipeline(db.QueryRowContext(ctx, `SELECT `+pipelineColumns+` FROM pipelines WHERE hook_id = ?`, hookID))
	if errors.Is(err, sql.ErrNoRows) {
		return Pipeline{}, ErrNotFound
	}
	return p, err
}

// ListPipelines — все конвейеры по имени.
func (db *DB) ListPipelines(ctx context.Context) ([]Pipeline, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+pipelineColumns+` FROM pipelines ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pipeline{}
	for rows.Next() {
		p, err := scanPipeline(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PipelineVersions — редакции без текста.
func (db *DB) PipelineVersions(ctx context.Context, id int64) ([]PipelineVersion, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, pipeline_id, ts, author, note FROM pipeline_versions WHERE pipeline_id = ? ORDER BY id DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PipelineVersion{}
	for rows.Next() {
		var v PipelineVersion
		if err := rows.Scan(&v.ID, &v.PipelineID, &v.TS, &v.Author, &v.Note); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PipelineVersion — одна редакция с текстом.
func (db *DB) PipelineVersion(ctx context.Context, id int64) (PipelineVersion, error) {
	var v PipelineVersion
	err := db.QueryRowContext(ctx, `SELECT id, pipeline_id, ts, author, note, content FROM pipeline_versions WHERE id = ?`, id).
		Scan(&v.ID, &v.PipelineID, &v.TS, &v.Author, &v.Note, &v.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}

const deploymentColumns = `id, pipeline_id, ref, commit_sha, tag, trigger, author, job_id, status, error, created_at, finished_at`

func scanDeployment(row interface{ Scan(...any) error }) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.PipelineID, &d.Ref, &d.Commit, &d.Tag, &d.Trigger, &d.Author, &d.JobID, &d.Status, &d.Error, &d.CreatedAt, &d.FinishedAt)
	return d, err
}

// CreateDeployment заводит выкладку в очереди.
func (db *DB) CreateDeployment(ctx context.Context, d Deployment) (int64, error) {
	res, err := db.ExecContext(ctx, `INSERT INTO deployments (pipeline_id, ref, commit_sha, tag, trigger, author, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, d.PipelineID, d.Ref, d.Commit, d.Tag, d.Trigger, d.Author, DeployQueued, FormatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetDeploymentJob привязывает задание.
func (db *DB) SetDeploymentJob(ctx context.Context, id, jobID int64) error {
	_, err := db.ExecContext(ctx, `UPDATE deployments SET job_id = ? WHERE id = ?`, jobID, id)
	return err
}

// UpdateDeployment — ход и итог выкладки (коммит — когда стал известен).
func (db *DB) UpdateDeployment(ctx context.Context, id int64, status, commit, errText string) error {
	fin := ""
	if status == DeploySucceeded || status == DeployFailed {
		fin = FormatTime(time.Now())
	}
	_, err := db.ExecContext(ctx, `UPDATE deployments SET status = ?, commit_sha = CASE WHEN ? = '' THEN commit_sha ELSE ? END, error = ?, finished_at = ? WHERE id = ?`,
		status, commit, commit, errText, fin, id)
	return err
}

// DeploymentByID — выкладка.
func (db *DB) DeploymentByID(ctx context.Context, id int64) (Deployment, error) {
	d, err := scanDeployment(db.QueryRowContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// Deployments — выкладки конвейера, свежие сверху.
func (db *DB) Deployments(ctx context.Context, pipelineID int64, limit int) ([]Deployment, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE pipeline_id = ? ORDER BY id DESC LIMIT ?`, pipelineID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ActiveDeployment — идёт ли выкладка конвейера.
func (db *DB) ActiveDeployment(ctx context.Context, pipelineID int64) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE pipeline_id = ? AND status IN (?, ?)`, pipelineID, DeployQueued, DeployRunning).Scan(&n)
	return n > 0, err
}

// RememberDelivery записывает доставку вебхука; false — она уже была.
// Записи старше суток убираются тут же.
func (db *DB) RememberDelivery(ctx context.Context, id string) (bool, error) {
	_, _ = db.ExecContext(ctx, `DELETE FROM hook_deliveries WHERE ts < ?`, FormatTime(time.Now().Add(-24*time.Hour)))
	res, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO hook_deliveries (id, ts) VALUES (?, ?)`, id, FormatTime(time.Now()))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
