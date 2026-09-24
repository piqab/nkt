package store

import (
	"context"
	"database/sql"
	"time"
)

// Кэш ответов модели и счётчик расхода.
//
// Кэш нужен не ради скорости, а ради денег и смысла: одна и та же
// находка встречается на десяти хостах, и платить за неё десять раз
// незачем — ответ зависит от текста находки, модели и языка, а не от
// того, на каком хосте её нашли. Ключ считает internal/ai.CacheKey.
//
// Счётчик — суточный, общий для всех хостов и пользователей: лимит
// задаётся один раз на хабе (см. ai.Settings.DailyLimit).

// AICacheGet возвращает сохранённый ответ модели.
func (d *DB) AICacheGet(ctx context.Context, key string) (string, bool, error) {
	var answer string
	err := d.QueryRowContext(ctx, `SELECT answer FROM ai_cache WHERE key = ?`, key).Scan(&answer)
	switch {
	case err == sql.ErrNoRows:
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	// Отметка использования — чтобы чистка сносила то, к чему давно не
	// возвращались, а не то, что давно создано.
	_, _ = d.ExecContext(ctx, `UPDATE ai_cache SET used_at = ? WHERE key = ?`, Now(), key)
	return answer, true, nil
}

// AICachePut сохраняет ответ модели.
func (d *DB) AICachePut(ctx context.Context, key, kind, model, lang, answer string) error {
	now := Now()
	_, err := d.ExecContext(ctx,
		`INSERT INTO ai_cache(key, kind, model, lang, answer, created_at, used_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET answer = excluded.answer, used_at = excluded.used_at`,
		key, kind, model, lang, answer, now, now)
	return err
}

// AICacheClear убирает весь кэш (смена модели или провайдера делает
// прежние ответы чужими).
// AIAnswer — сохранённый ответ модели по находке на хосте.
type AIAnswer struct {
	Key       string `json:"key"`
	HostID    int64  `json:"host_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Object    string `json:"object"`
	File      string `json:"file"`
	Model     string `json:"model"`
	Lang      string `json:"lang"`
	Prompt    string `json:"prompt"`
	Answer    string `json:"answer"`
	CreatedAt string `json:"created_at"`
}

// AIAnswerRef — ссылка на сохранённый ответ без текста: интерфейс по ней
// красит лампочки, сравнивая вид, заголовок, объект и файл строки.
type AIAnswerRef struct {
	Key       string `json:"key"`
	HostID    int64  `json:"host_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Object    string `json:"object"`
	File      string `json:"file"`
	CreatedAt string `json:"created_at"`
}

// AIAnswerPut сохраняет ответ; повторный запрос по той же находке на том
// же хосте заменяет прежний.
func (d *DB) AIAnswerPut(ctx context.Context, a AIAnswer) error {
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	_, err := d.ExecContext(ctx,
		`INSERT INTO ai_answers(key, host_id, kind, title, object, file, model, lang, prompt, answer, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key, host_id) DO UPDATE SET
		   kind = excluded.kind, title = excluded.title, object = excluded.object, file = excluded.file,
		   model = excluded.model, lang = excluded.lang, prompt = excluded.prompt, answer = excluded.answer,
		   created_at = excluded.created_at`,
		a.Key, a.HostID, a.Kind, a.Title, a.Object, a.File, a.Model, a.Lang, a.Prompt, a.Answer, a.CreatedAt)
	return err
}

const aiAnswerCols = `key, host_id, kind, title, object, file, model, lang, prompt, answer, created_at`

func scanAIAnswer(row interface{ Scan(dest ...any) error }) (AIAnswer, error) {
	var a AIAnswer
	err := row.Scan(&a.Key, &a.HostID, &a.Kind, &a.Title, &a.Object, &a.File, &a.Model, &a.Lang, &a.Prompt, &a.Answer, &a.CreatedAt)
	return a, err
}

// AIAnswerGet — ответ по находке на этом хосте.
func (d *DB) AIAnswerGet(ctx context.Context, key string, hostID int64) (AIAnswer, bool, error) {
	a, err := scanAIAnswer(d.QueryRowContext(ctx, `SELECT `+aiAnswerCols+` FROM ai_answers WHERE key = ? AND host_id = ?`, key, hostID))
	switch {
	case err == sql.ErrNoRows:
		return AIAnswer{}, false, nil
	case err != nil:
		return AIAnswer{}, false, err
	}
	return a, true, nil
}

// AIAnswerOther — самый свежий ответ по той же находке на другом хосте:
// «такая уже была» — показать его первым, а спрашивать заново по кнопке.
func (d *DB) AIAnswerOther(ctx context.Context, key string, hostID int64) (AIAnswer, bool, error) {
	a, err := scanAIAnswer(d.QueryRowContext(ctx,
		`SELECT `+aiAnswerCols+` FROM ai_answers WHERE key = ? AND host_id != ? ORDER BY created_at DESC LIMIT 1`, key, hostID))
	switch {
	case err == sql.ErrNoRows:
		return AIAnswer{}, false, nil
	case err != nil:
		return AIAnswer{}, false, err
	}
	return a, true, nil
}

// AIAnswerRefs — все сохранённые ответы без текста.
func (d *DB) AIAnswerRefs(ctx context.Context) ([]AIAnswerRef, error) {
	rows, err := d.QueryContext(ctx, `SELECT key, host_id, kind, title, object, file, created_at FROM ai_answers ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIAnswerRef
	for rows.Next() {
		var r AIAnswerRef
		if err := rows.Scan(&r.Key, &r.HostID, &r.Kind, &r.Title, &r.Object, &r.File, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AIAnswerDelete убирает сохранённый ответ.
func (d *DB) AIAnswerDelete(ctx context.Context, key string, hostID int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM ai_answers WHERE key = ? AND host_id = ?`, key, hostID)
	return err
}

func (d *DB) AICacheClear(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `DELETE FROM ai_answers`); err != nil {
		return err
	}
	_, err := d.ExecContext(ctx, `DELETE FROM ai_cache`)
	return err
}

// AICacheSize — сколько ответов лежит в кэше.
func (d *DB) AICacheSize(ctx context.Context) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM ai_answers) + (SELECT COUNT(*) FROM ai_cache)`).Scan(&n)
	return n, err
}

// AIUsageToday — сколько запросов к модели ушло сегодня (UTC).
func (d *DB) AIUsageToday(ctx context.Context) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT COALESCE(requests, 0) FROM ai_usage WHERE day = ?`, today()).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// AIUsageAdd отмечает один ушедший запрос (кэш не считается).
func (d *DB) AIUsageAdd(ctx context.Context) error {
	_, err := d.ExecContext(ctx,
		`INSERT INTO ai_usage(day, requests) VALUES(?, 1)
		 ON CONFLICT(day) DO UPDATE SET requests = requests + 1`, today())
	return err
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

// AIReview — сохранённый архитектурный разбор карты ресурсов.
type AIReview struct {
	ID int64 `json:"id"`
	// Scope — «host:<id>» или «hub» для разбора всех хостов сразу.
	Scope     string `json:"scope"`
	Model     string `json:"model"`
	Lang      string `json:"lang"`
	Answer    string `json:"answer"`
	CreatedAt string `json:"created_at"`
	Author    string `json:"author"`
}

// AIReviewAdd сохраняет разбор: по ним видно, что изменилось с прошлого
// раза, — ради этого они и хранятся, а не только показываются.
func (d *DB) AIReviewAdd(ctx context.Context, r AIReview) (int64, error) {
	res, err := d.ExecContext(ctx,
		`INSERT INTO ai_reviews(scope, model, lang, answer, created_at, author) VALUES(?, ?, ?, ?, ?, ?)`,
		r.Scope, r.Model, r.Lang, r.Answer, Now(), r.Author)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AIReviews — последние разборы для области (scope), новые первыми.
func (d *DB) AIReviews(ctx context.Context, scope string, limit int) ([]AIReview, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := d.QueryContext(ctx,
		`SELECT id, scope, model, lang, answer, created_at, author FROM ai_reviews
		 WHERE scope = ? ORDER BY id DESC LIMIT ?`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIReview{}
	for rows.Next() {
		var r AIReview
		if err := rows.Scan(&r.ID, &r.Scope, &r.Model, &r.Lang, &r.Answer, &r.CreatedAt, &r.Author); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
