package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Job statuses.
const (
	// JobQueued — задание принято, но на его ключе очереди уже что-то
	// выполняется.
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCanceled  = "canceled"
	// JobInterrupted — служба перезапустилась посреди работы. Отдельный
	// статус, а не «ошибка»: причина не в задании, и продолжить его часто
	// можно ровно с того места, где оборвались.
	JobInterrupted = "interrupted"
)

// Job — одно фоновое дело: применение профиля, скачивание образа,
// создание машины.
type Job struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Queue  string `json:"queue"`
	Status string `json:"status"`
	// Params — вход задания, как его задал оператор; Resume — что уже
	// сделано. Оба JSON, но их разбирает исполнитель своего вида, а не
	// хранилище.
	Params     string `json:"params,omitempty"`
	Resume     string `json:"resume,omitempty"`
	Step       int    `json:"step"`
	Steps      int    `json:"steps"`
	StepName   string `json:"step_name,omitempty"`
	Error      string `json:"error,omitempty"`
	Author     string `json:"author,omitempty"`
	CreatedAt  string `json:"created_at"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// Done отвечает, закончилось ли задание. Прерванное считается
// законченным: оно точно не выполняется — продолжение заводит новое.
func (j Job) Done() bool {
	switch j.Status {
	case JobSucceeded, JobFailed, JobCanceled, JobInterrupted:
		return true
	}
	return false
}

// JobLogLine — строка журнала задания.
type JobLogLine struct {
	Seq  int64  `json:"seq"`
	TS   string `json:"ts"`
	Text string `json:"text"`
}

// CreateJob заводит задание в очереди.
func (db *DB) CreateJob(ctx context.Context, j Job) (int64, error) {
	if j.Status == "" {
		j.Status = JobQueued
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO jobs (kind, title, queue, status, params, resume, step, steps, step_name,
		                  error, author, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
		j.Kind, j.Title, j.Queue, j.Status, j.Params, j.Resume, j.Step, j.Steps, j.StepName,
		j.Author, FormatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// JobByID возвращает одно задание.
func (db *DB) JobByID(ctx context.Context, id int64) (Job, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, kind, title, queue, status, params, resume, step, steps, step_name,
		       error, author, created_at, started_at, finished_at
		FROM jobs WHERE id = ?`, id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

// ListJobs отдаёт последние задания, новые сверху.
func (db *DB) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, kind, title, queue, status, params, resume, step, steps, step_name,
		       error, author, created_at, started_at, finished_at
		FROM jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// UnfinishedJobs отдаёт задания, которые числятся идущими или ждущими
// очереди — то, что нужно разобрать при запуске службы.
func (db *DB) UnfinishedJobs(ctx context.Context) ([]Job, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, kind, title, queue, status, params, resume, step, steps, step_name,
		       error, author, created_at, started_at, finished_at
		FROM jobs WHERE status IN (?, ?) ORDER BY id`, JobQueued, JobRunning)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// CountActiveJobs считает незавершённые задания — то число, что стоит в
// меню. Отдельным запросом, а не подсчётом по выданной странице: страница
// короткая, и счёт по ней врал бы ровно тогда, когда заданий много.
func (db *DB) CountActiveJobs(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE status IN (?, ?)`, JobQueued, JobRunning).Scan(&n)
	return n, err
}

// MarkJobRunning отмечает начало работы.
func (db *DB) MarkJobRunning(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, started_at = ?, error = '' WHERE id = ?`,
		JobRunning, FormatTime(time.Now()), id)
	return err
}

// FinishJob проставляет исход. Пустой errMsg — успех, если статус не
// задан явно вызывающим.
func (db *DB) FinishJob(ctx context.Context, id int64, status, errMsg string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errMsg, FormatTime(time.Now()), id)
	return err
}

// SetJobStep запоминает, на каком шаге задание, — чтобы это пережило
// перезапуск и было видно в списке без чтения всего журнала.
func (db *DB) SetJobStep(ctx context.Context, id int64, step, steps int, name string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE jobs SET step = ?, steps = ?, step_name = ? WHERE id = ?`, step, steps, name, id)
	return err
}

// SetJobResume сохраняет состояние продолжения.
func (db *DB) SetJobResume(ctx context.Context, id int64, resume string) error {
	_, err := db.ExecContext(ctx, `UPDATE jobs SET resume = ? WHERE id = ?`, resume, id)
	return err
}

// AppendJobLog дописывает строку журнала. Номер строки выдаётся здесь же,
// одним запросом с вставкой: параллельных писателей у одного задания нет
// (его выполняет одна горутина), а гонки с чтением так не возникает.
func (db *DB) AppendJobLog(ctx context.Context, id int64, text string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO job_log (job_id, seq, ts, text)
		VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM job_log WHERE job_id = ?), ?, ?)`,
		id, id, FormatTime(time.Now()), text)
	return err
}

// JobLog отдаёт журнал задания начиная с номера after — так вернувшийся
// браузер догружает только то, чего у него ещё нет.
func (db *DB) JobLog(ctx context.Context, id int64, after int64, limit int) ([]JobLogLine, error) {
	if limit <= 0 || limit > 5000 {
		limit = 2000
	}
	rows, err := db.QueryContext(ctx,
		`SELECT seq, ts, text FROM job_log WHERE job_id = ? AND seq > ? ORDER BY seq LIMIT ?`,
		id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobLogLine{}
	for rows.Next() {
		var l JobLogLine
		if err := rows.Scan(&l.Seq, &l.TS, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteOldJobs убирает завершённые задания старше срока — журнал одного
// применения профиля невелик, но копится годами.
func (db *DB) DeleteOldJobs(ctx context.Context, olderThan time.Time) (int64, error) {
	cutoff := FormatTime(olderThan)
	res, err := db.ExecContext(ctx,
		`DELETE FROM job_log WHERE job_id IN
			(SELECT id FROM jobs WHERE finished_at != '' AND finished_at < ?)`, cutoff)
	if err != nil {
		return 0, err
	}
	_ = res
	res, err = db.ExecContext(ctx,
		`DELETE FROM jobs WHERE finished_at != '' AND finished_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanJob(row rowScanner) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.Title, &j.Queue, &j.Status, &j.Params, &j.Resume,
		&j.Step, &j.Steps, &j.StepName, &j.Error, &j.Author, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
	return j, err
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
