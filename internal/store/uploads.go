package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Загрузки в «Диски → Файлы».

// Состояния загрузки.
const (
	UploadOpen       = "open"
	UploadDone       = "done"
	UploadRolledBack = "rolled_back"
)

// FileUpload — одна загрузка (файл или папка) с числом позиций.
type FileUpload struct {
	ID      int64  `json:"id"`
	TS      string `json:"ts"`
	Author  string `json:"author"`
	Dir     string `json:"dir"`
	Note    string `json:"note"`
	Status  string `json:"status"`
	New     int    `json:"new"`
	Changed int    `json:"changed"`
}

// FileUploadItem — файл загрузки.
type FileUploadItem struct {
	ID        int64  `json:"id"`
	UploadID  int64  `json:"upload_id"`
	Path      string `json:"path"`
	Existed   bool   `json:"existed"`
	VersionID int64  `json:"version_id"`
	Size      int64  `json:"size"`
	SHAAfter  string `json:"sha_after"`
}

// CreateUpload заводит загрузку.
func (d *DB) CreateUpload(ctx context.Context, author, dir, note string) (int64, error) {
	res, err := d.ExecContext(ctx, `INSERT INTO file_uploads(ts, author, dir, note, status) VALUES(?, ?, ?, ?, ?)`,
		Now(), author, dir, note, UploadOpen)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddUploadItem — файл лёг в рамках загрузки.
func (d *DB) AddUploadItem(ctx context.Context, it FileUploadItem) error {
	existed := 0
	if it.Existed {
		existed = 1
	}
	_, err := d.ExecContext(ctx, `INSERT INTO file_upload_items(upload_id, path, existed, version_id, size, sha_after) VALUES(?, ?, ?, ?, ?, ?)`,
		it.UploadID, it.Path, existed, it.VersionID, it.Size, it.SHAAfter)
	return err
}

// SetUploadStatus — загрузка закончена или откачена.
func (d *DB) SetUploadStatus(ctx context.Context, id int64, status string) error {
	_, err := d.ExecContext(ctx, `UPDATE file_uploads SET status = ? WHERE id = ?`, status, id)
	return err
}

const uploadColumns = `u.id, u.ts, u.author, u.dir, u.note, u.status,
	(SELECT COUNT(*) FROM file_upload_items i WHERE i.upload_id = u.id AND i.existed = 0),
	(SELECT COUNT(*) FROM file_upload_items i WHERE i.upload_id = u.id AND i.existed = 1)`

func scanUpload(row interface{ Scan(...any) error }) (FileUpload, error) {
	var u FileUpload
	err := row.Scan(&u.ID, &u.TS, &u.Author, &u.Dir, &u.Note, &u.Status, &u.New, &u.Changed)
	return u, err
}

// UploadByID — одна загрузка.
func (d *DB) UploadByID(ctx context.Context, id int64) (FileUpload, error) {
	u, err := scanUpload(d.QueryRowContext(ctx, `SELECT `+uploadColumns+` FROM file_uploads u WHERE u.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// ListUploads — загрузки в каталог dir и его подкаталоги, новые сверху.
func (d *DB) ListUploads(ctx context.Context, dir string, limit int) ([]FileUpload, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	prefix := strings.TrimRight(dir, "/") + "/"
	rows, err := d.QueryContext(ctx, `SELECT `+uploadColumns+` FROM file_uploads u
		WHERE u.dir = ? OR substr(u.dir, 1, ?) = ? ORDER BY u.id DESC LIMIT ?`, dir, len(prefix), prefix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FileUpload{}
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UploadItems — файлы загрузки.
func (d *DB) UploadItems(ctx context.Context, id int64) ([]FileUploadItem, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, upload_id, path, existed, version_id, size, sha_after
		FROM file_upload_items WHERE upload_id = ? ORDER BY path`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FileUploadItem{}
	for rows.Next() {
		var it FileUploadItem
		var existed int
		if err := rows.Scan(&it.ID, &it.UploadID, &it.Path, &existed, &it.VersionID, &it.Size, &it.SHAAfter); err != nil {
			return nil, err
		}
		it.Existed = existed == 1
		out = append(out, it)
	}
	return out, rows.Err()
}

// DeleteUpload — запись загрузки (файлы на диске не трогаются).
func (d *DB) DeleteUpload(ctx context.Context, id int64) error {
	if _, err := d.ExecContext(ctx, `DELETE FROM file_upload_items WHERE upload_id = ?`, id); err != nil {
		return err
	}
	_, err := d.ExecContext(ctx, `DELETE FROM file_uploads WHERE id = ?`, id)
	return err
}

// DeleteVersions — версии по ID; имена их содержимого — чтобы убрать
// то, на что больше никто не ссылается.
func (d *DB) DeleteVersions(ctx context.Context, ids []int64) ([]string, error) {
	var blobs []string
	for _, id := range ids {
		v, err := d.VersionByID(ctx, id)
		if err != nil {
			continue
		}
		if _, err := d.ExecContext(ctx, `DELETE FROM config_versions WHERE id = ?`, id); err != nil {
			return blobs, err
		}
		_, _ = d.ExecContext(ctx, `UPDATE file_upload_items SET version_id = 0 WHERE version_id = ?`, id)
		var refs int
		if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM config_versions WHERE blob_name = ?`, v.BlobName).Scan(&refs); err == nil && refs == 0 {
			blobs = append(blobs, v.BlobName)
		}
	}
	return blobs, nil
}

// FileHistoryVersions — версии истории файлов (загрузки и правки в
// «Файлах», не конфигураций): старые первыми, для вытеснения.
func (d *DB) FileHistoryVersions(ctx context.Context) ([]ConfigVersion, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+versionColumns+` FROM config_versions
		WHERE service = 'files' OR action = ? ORDER BY id ASC`, ActionUpload)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConfigVersion{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
