package vmimage

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// partSuffix — недокачанный файл. Отдельное имя, а не запись сразу в
// целевой файл: недокачанный образ, лежащий под своим именем, однажды
// уйдёт в машину как настоящий.
const partSuffix = ".part"

// downloadTimeout — потолок на одно скачивание. Образы весят сотни
// мегабайт, и даже на медленном канале час — это уже не «медленно», а
// «что-то не так».
const downloadTimeout = time.Hour

// Store хранит скачанные образы.
//
// Каталог свой, а не /var/lib/libvirt/images: туда nkt из-под своего
// юнита писать не может, да и кэш образов — его собственные данные,
// которые он же и чистит.
type Store struct {
	dir    string
	client *http.Client
}

// NewStore строит хранилище в указанном каталоге.
func NewStore(dir string) *Store {
	return &Store{
		dir: dir,
		// Без общего таймаута на клиенте: он рубил бы и само скачивание,
		// а не только установление соединения. Срок жизни задаётся
		// контекстом запроса.
		client: &http.Client{},
	}
}

// Dir отдаёт каталог кэша.
func (s *Store) Dir() string { return s.dir }

// Local — что известно о скачанном образе.
type Local struct {
	ID string `json:"id"`
	// Path пуст, если образа нет.
	Path       string `json:"path,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Downloaded bool   `json:"downloaded"`
	// Partial — есть недокачанный кусок: скачивание можно продолжить.
	Partial     bool  `json:"partial,omitempty"`
	PartialSize int64 `json:"partial_size,omitempty"`
	ModTime     string `json:"mod_time,omitempty"`
}

// Status отвечает, что из каталога уже скачано.
func (s *Store) Status() []Local {
	out := make([]Local, 0, len(Catalog))
	for _, img := range Catalog {
		l := Local{ID: img.ID}
		full := s.Path(img)
		if st, err := os.Stat(full); err == nil {
			l.Downloaded, l.Path, l.Size = true, full, st.Size()
			l.ModTime = st.ModTime().UTC().Format(time.RFC3339)
		} else if st, err := os.Stat(full + partSuffix); err == nil {
			l.Partial, l.PartialSize = true, st.Size()
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Path — куда ложится образ.
func (s *Store) Path(img Image) string { return filepath.Join(s.dir, img.FileName) }

// Have отвечает, лежит ли образ в кэше.
func (s *Store) Have(img Image) bool {
	st, err := os.Stat(s.Path(img))
	return err == nil && st.Size() > 0
}

// Delete убирает скачанный образ и недокачанный кусок.
func (s *Store) Delete(img Image) error {
	full := s.Path(img)
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(full + partSuffix); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Progress — как идёт скачивание.
type Progress struct {
	Done  int64
	Total int64
}

// Download качает образ и проверяет контрольную сумму.
//
// Докачивает: если рядом лежит недокачанный кусок, запрашивается остаток
// (Range), а не файл целиком. Это и делает задание продолжаемым после
// перезапуска службы — на сотнях мегабайт разница осязаемая.
//
// Сумма проверяется всегда и по готовому файлу целиком, а не по ходу:
// после докачки посчитать её «на лету» всё равно нельзя, а образ,
// которому нельзя доверять, опаснее ещё одного прохода по диску.
func (s *Store) Download(ctx context.Context, img Image, report func(Progress)) (string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("создание каталога образов: %w", err)
	}
	full := s.Path(img)
	part := full + partSuffix

	want, err := s.expectedChecksum(ctx, img)
	if err != nil {
		return "", err
	}

	// Готовый файл проверяется заново: он мог остаться от прошлой
	// сборки образа, за которой стоит та же ссылка «latest».
	if _, err := os.Stat(full); err == nil {
		sum, err := checksumFile(full, img.ChecksumKind)
		if err == nil && sum == want {
			return full, nil
		}
		if err := os.Remove(full); err != nil {
			return "", err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	if err := s.fetch(ctx, img, part, report); err != nil {
		return "", err
	}

	sum, err := checksumFile(part, img.ChecksumKind)
	if err != nil {
		return "", err
	}
	if sum != want {
		// Битый кусок не оставляем: иначе следующая попытка «докачает»
		// его с середины и получит ту же несходящуюся сумму.
		_ = os.Remove(part)
		return "", fmt.Errorf("контрольная сумма не сошлась: получено %s, ожидалось %s", sum, want)
	}
	if err := os.Rename(part, full); err != nil {
		return "", err
	}
	return full, nil
}

// expectedChecksum читает сумму из файла сумм рядом с образом.
func (s *Store) expectedChecksum(ctx context.Context, img Image) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.ChecksumURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("файл сумм: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("файл сумм: код %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return parseChecksums(string(body), img.FileName)
}

// fetch качает файл, продолжая с того места, где остановились.
func (s *Store) fetch(ctx context.Context, img Image, part string, report func(Progress)) error {
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.URL, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		// Сервер не поддержал докачку и отдал файл целиком — значит и
		// писать надо с начала, а не дописывать в хвост уже скачанному.
		flags |= os.O_TRUNC
		have = 0
	default:
		return fmt.Errorf("скачивание: код %d", resp.StatusCode)
	}

	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	total := resp.ContentLength
	if total > 0 {
		total += have
	}
	written, err := copyWithProgress(ctx, f, resp.Body, have, total, report)
	if err != nil {
		return err
	}
	if total > 0 && written != total {
		return fmt.Errorf("скачано %d байт из %d", written, total)
	}
	return f.Sync()
}

// copyWithProgress копирует, изредка сообщая о ходе дела.
func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader,
	done, total int64, report func(Progress)) (int64, error) {

	buf := make([]byte, 1<<20)
	lastReport := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return done, werr
			}
			done += int64(n)
			// Не чаще раза в секунду: журнал задания читает человек, и
			// сто строк в секунду ему не нужны.
			if report != nil && time.Since(lastReport) > time.Second {
				report(Progress{Done: done, Total: total})
				lastReport = time.Now()
			}
		}
		if err == io.EOF {
			if report != nil {
				report(Progress{Done: done, Total: total})
			}
			return done, nil
		}
		if err != nil {
			return done, err
		}
	}
}

// checksumFile считает сумму готового файла.
func checksumFile(path, kind string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var h hash.Hash
	switch strings.ToLower(kind) {
	case SHA512:
		h = sha512.New()
	default:
		h = sha256.New()
	}
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
