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
func (d *DB) AICacheClear(ctx context.Context) error {
	_, err := d.ExecContext(ctx, `DELETE FROM ai_cache`)
	return err
}

// AICacheSize — сколько ответов лежит в кэше.
func (d *DB) AICacheSize(ctx context.Context) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM ai_cache`).Scan(&n)
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
