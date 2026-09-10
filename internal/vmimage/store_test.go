package vmimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// imageServer изображает зеркало образов: отдаёт файл сумм и сам файл,
// поддерживая докачку через Range — ровно как cloud.debian.org.
func imageServer(t *testing.T, body []byte, allowRange bool) (*httptest.Server, Image) {
	t.Helper()
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		// Рядом лежит и другой образ — чтобы проверить, что берётся
		// строка нужного файла, а не первая попавшаяся.
		fmt.Fprintf(w, "%s  other-image.qcow2\n%s  image.qcow2\n", strings.Repeat("f", 64), digest)
	})
	mux.HandleFunc("/image.qcow2", func(w http.ResponseWriter, r *http.Request) {
		rangeHdr := r.Header.Get("Range")
		if !allowRange || rangeHdr == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}
		var from int64
		fmt.Sscanf(rangeHdr, "bytes=%d-", &from)
		if from >= int64(len(body)) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		rest := body[from:]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(body)-1, len(body)))
		w.Header().Set("Content-Length", strconv.Itoa(len(rest)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(rest)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, Image{
		ID: "test", FileName: "image.qcow2", ChecksumKind: SHA256,
		URL: srv.URL + "/image.qcow2", ChecksumURL: srv.URL + "/SHA256SUMS",
	}
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	body := []byte(strings.Repeat("образ", 1000))
	srv, img := imageServer(t, body, true)

	downloads := 0
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".qcow2") {
			downloads++
		}
		inner.ServeHTTP(w, r)
	})

	store := NewStore(t.TempDir())
	path, err := store.Download(context.Background(), img, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("скачано не то (%d байт вместо %d)", len(got), len(body))
	}
	// Недокачанный кусок не должен остаться рядом.
	if _, err := os.Stat(path + partSuffix); !os.IsNotExist(err) {
		t.Error("остался недокачанный кусок")
	}
	// Повторный вызов не качает заново: сумма уже сошлась, файл на
	// месте. Это и делает задание безопасным для повтора после
	// перезапуска службы.
	before := downloads
	if _, err := store.Download(context.Background(), img, nil); err != nil {
		t.Fatalf("повторный Download: %v", err)
	}
	if downloads != before {
		t.Errorf("образ скачан заново, хотя уже лежал рядом (запросов: %d → %d)", before, downloads)
	}
}

// Оборванная закачка продолжается с места обрыва, а не начинается заново
// — на сотнях мегабайт разница осязаемая.
func TestDownloadResumesFromPart(t *testing.T) {
	body := []byte(strings.Repeat("x", 4096))
	srv, img := imageServer(t, body, true)

	dir := t.TempDir()
	store := NewStore(dir)
	// Так выглядит каталог после обрыва: половина файла уже на диске.
	part := filepath.Join(dir, img.FileName+partSuffix)
	if err := os.WriteFile(part, body[:2048], 0o644); err != nil {
		t.Fatalf("подготовка куска: %v", err)
	}

	var ranges []string
	srv.Config.Handler = logRange(srv.Config.Handler, &ranges)

	path, err := store.Download(context.Background(), img, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Errorf("после докачки файл не совпал (%d байт)", len(got))
	}
	if len(ranges) == 0 || ranges[0] != "bytes=2048-" {
		t.Errorf("докачка не запрошена: %q", ranges)
	}
}

// Сервер без поддержки докачки отдаёт файл целиком — тогда и писать надо
// с начала, иначе к остатку допишется дубль и сумма не сойдётся.
func TestDownloadHandlesServerWithoutRange(t *testing.T) {
	body := []byte(strings.Repeat("y", 4096))
	_, img := imageServer(t, body, false)

	dir := t.TempDir()
	store := NewStore(dir)
	if err := os.WriteFile(filepath.Join(dir, img.FileName+partSuffix), body[:1000], 0o644); err != nil {
		t.Fatalf("подготовка куска: %v", err)
	}
	path, err := store.Download(context.Background(), img, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(body) {
		t.Errorf("файл собран неверно: %d байт", len(got))
	}
}

// Несовпавшая сумма — отказ, и битый кусок стирается: иначе следующая
// попытка «докачает» его и получит ту же несходящуюся сумму.
func TestDownloadRejectsBadChecksum(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		// Сумма от другого содержимого — так выглядит подменённый или
		// побитый по дороге образ.
		fmt.Fprintf(w, "%s  image.qcow2\n", strings.Repeat("a", 64))
	})
	mux.HandleFunc("/image.qcow2", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("совсем другое содержимое"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	img := Image{
		ID: "test", FileName: "image.qcow2", ChecksumKind: SHA256,
		URL: srv.URL + "/image.qcow2", ChecksumURL: srv.URL + "/SHA256SUMS",
	}
	store := NewStore(t.TempDir())
	_, err := store.Download(context.Background(), img, nil)
	if err == nil {
		t.Fatal("образ с несошедшейся суммой принят")
	}
	if !strings.Contains(err.Error(), "сумма") {
		t.Errorf("ошибка = %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), img.FileName+partSuffix)); !os.IsNotExist(err) {
		t.Error("битый кусок остался на диске")
	}
}

func TestParseChecksums(t *testing.T) {
	body := "aaa  first.qcow2\nbbb *second.qcow2\n"
	if got, err := parseChecksums(body, "second.qcow2"); err != nil || got != "bbb" {
		t.Errorf("parseChecksums = %q, %v", got, err)
	}
	// Имя сравнивается целиком: рядом лежат образы с похожими именами.
	if _, err := parseChecksums(body, "cond.qcow2"); err == nil {
		t.Error("совпадение по части имени принято")
	}
}

// logRange запоминает заголовки Range, с которыми пришли за файлом.
func logRange(next http.Handler, out *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Range"); h != "" {
			*out = append(*out, h)
		}
		next.ServeHTTP(w, r)
	})
}
