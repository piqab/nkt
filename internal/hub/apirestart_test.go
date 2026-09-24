package hub

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Отказ соединения через туннель — «API не поднялся», а не «SSH лежит» и
// не «неверный пароль».
func TestIsAPIDownAndTransientSSH(t *testing.T) {
	down := errors.New(`вход на хост "ii": запрос входа: Post "http://127.0.0.1:8077/api/auth/login": ssh: rejected: connect failed ("Connection refused")`)
	if !isAPIDown(down) {
		t.Error("connect failed через туннель должен считаться «API не поднялся»")
	}
	if isAPIDown(errors.New("ssh: handshake failed: ssh: unable to authenticate")) {
		t.Error("неверный ключ — не перезапуск API")
	}
	if isAPIDown(errors.New("вход не удался (код 401)")) {
		t.Error("401 — не перезапуск API")
	}
	if !isTransientSSHError(errors.New("dial tcp 10.0.0.5:22: connect: connection refused")) {
		t.Error("connection refused по SSH — временная ошибка")
	}
	if isTransientSSHError(errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [publickey]")) {
		t.Error("отказ аутентификации — не временная ошибка")
	}
}

// Прокси ждёт перезапуск API хоста, который недавно отвечал: первые
// попытки входа получают отказ соединения, потом API поднимается — и
// запрос проходит, а не падает с «connection refused».
func TestCookieForWithWaitSurvivesAPIRestart(t *testing.T) {
	ctx := context.Background()
	m, db := newTestManager(t)
	id, err := m.AddHost(ctx, "web-1", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	pwEnc, _ := secretbox.Encrypt(m.key, []byte("admin-pw"))
	if err := db.SetHostAdmin(ctx, id, "admin", pwEnc); err != nil {
		t.Fatal(err)
	}
	if err := db.TouchHostSeen(ctx, id); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "c1"})
	}))
	defer srv.Close()
	refusals := 2
	dial := func(network, addr string) (net.Conn, error) {
		if refusals > 0 {
			refusals--
			return nil, errors.New(`ssh: rejected: connect failed ("Connection refused")`)
		}
		return net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	}
	started := time.Now()
	cookie, err := m.cookieForWithWait(ctx, id, dial)
	if err != nil || cookie != "c1" {
		t.Fatalf("cookie=%q err=%v", cookie, err)
	}
	if took := time.Since(started); took < apiRestartPoll || took > apiRestartWait {
		t.Errorf("ожидание %v не похоже на две паузы по %v", took, apiRestartPoll)
	}
	// Хост, который давно не отвечал, ждать не надо — ошибка сразу.
	// (Кэш сессии сбрасывается, как это делает onFail при обрыве.)
	m.dropSession(id)
	_, _ = db.ExecContext(ctx, `UPDATE hosts SET last_seen_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), id)
	refusals = 100
	started = time.Now()
	if _, err := m.cookieForWithWait(ctx, id, dial); err == nil || time.Since(started) > apiRestartPoll {
		t.Errorf("для давно молчащего хоста ожидания быть не должно: err=%v, %v", err, time.Since(started))
	}
}

// Одновременно идёт не больше maxParallelInstalls установок; лишние ждут
// и отпускаются, когда место освобождается.
func TestInstallSlots(t *testing.T) {
	ctx := context.Background()
	jobs := make([]*installJob, 0, maxParallelInstalls+1)
	for i := 0; i < maxParallelInstalls; i++ {
		j := &installJob{}
		if err := acquireInstallSlot(ctx, j); err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	extra := &installJob{}
	done := make(chan error, 1)
	go func() { done <- acquireInstallSlot(ctx, extra) }()
	select {
	case <-done:
		t.Fatal("лишняя установка не должна была пройти")
	case <-time.After(100 * time.Millisecond):
	}
	releaseInstallSlot()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(extra.events) == 0 || extra.events[0].Key != "hub.installQueued" {
		t.Errorf("ожидание должно быть в журнале: %+v", extra.events)
	}
	for range jobs {
		releaseInstallSlot()
	}
	// Отмена контекста снимает ожидание.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	for i := 0; i < maxParallelInstalls; i++ {
		_ = acquireInstallSlot(ctx, &installJob{})
	}
	if err := acquireInstallSlot(cctx, &installJob{}); err == nil {
		t.Error("отменённый контекст должен прерывать ожидание")
	}
	for i := 0; i < maxParallelInstalls; i++ {
		releaseInstallSlot()
	}
}

// Вывод диагностики сворачивается в одну строку: состояние юнита, адреса
// слушателей порта, хвост журнала.
func TestFormatAPIDiag(t *testing.T) {
	out := "activating\n--\nLISTEN 0 4096 127.0.0.1:8077 0.0.0.0:*\n--\nstarting server\nopen database: locked\n"
	got := formatAPIDiag(out, 8077)
	for _, want := range []string{"activating", "127.0.0.1:8077", "open database: locked", "starting server | open"} {
		if !strings.Contains(got, want) {
			t.Errorf("нет %q в %q", want, got)
		}
	}
	if got := formatAPIDiag("inactive\n--\n\n--\n", 8077); !strings.Contains(got, "inactive") || !strings.Contains(got, "никто") {
		t.Errorf("пустой слушатель: %q", got)
	}
}
