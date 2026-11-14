package hub

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Прямой путь к машине: успех запоминается и продлевает память, неудача
// помнится на порядок короче — только что созданная машина или секундная
// заминка не должны на десять минут отправлять хост на через-хост путь,
// который для macvtap-гостей мёртв.
func TestDirectProbeCacheTTL(t *testing.T) {
	m := &Manager{}
	// Порт 1 на loopback закрыт: любая настоящая проба даёт false и не
	// требует сети дальше localhost.
	host := store.Host{ID: 7, Addr: "127.0.0.1", SSHPort: 1}

	// Свежий положительный итог возвращается из памяти без пробы — иначе
	// проба до закрытого порта дала бы false.
	m.rememberDirect(host.ID, true)
	if !m.directReachable(host) {
		t.Error("свежий положительный итог пробы не возвращён из памяти")
	}

	// Положительный итог старше TTL перепробуется: закрытый порт — false.
	m.directCache[host.ID] = directProbe{ok: true, at: time.Now().Add(-directProbeTTL - time.Second)}
	if m.directReachable(host) {
		t.Error("устаревший положительный итог не перепробован")
	}

	// Отрицательный итог в пределах своего короткого TTL возвращается из
	// памяти: отметка времени не обновляется (пробы не было).
	at := time.Now().Add(-directProbeFailTTL / 2)
	m.directCache[host.ID] = directProbe{ok: false, at: at}
	if m.directReachable(host) {
		t.Error("свежий отрицательный итог вернул true")
	}
	if !m.directCache[host.ID].at.Equal(at) {
		t.Error("отметка времени изменилась — была проба вместо возврата из памяти")
	}

	// Отрицательный итог старше короткого TTL перепробуется: отметка
	// обновляется, даже если проба снова неудачна.
	old := time.Now().Add(-directProbeFailTTL - time.Second)
	m.directCache[host.ID] = directProbe{ok: false, at: old}
	m.directReachable(host)
	if !m.directCache[host.ID].at.After(old) {
		t.Error("устаревший отрицательный итог не перепробован")
	}
}

// Отказ учётным данным отличается от транспортного сбоя: по «no route to
// host» нельзя заключить, что ключ не работает, а по «unable to
// authenticate» — можно.
func TestSshAuthFailed(t *testing.T) {
	auth := errors.New(`ssh: handshake failed: unable to authenticate, attempted methods ["none" "publickey"]`)
	if !sshAuthFailed(auth) {
		t.Error("отказ учётным данным не распознан")
	}
	if sshAuthFailed(errors.New(`dial tcp 10.0.0.5:22: connect: no route to host`)) {
		t.Error("транспортная ошибка принята за отказ учётным данным")
	}
	if sshAuthFailed(nil) {
		t.Error("nil принят за отказ учётным данным")
	}
	// Комбинированная ошибка обоих путей (авто-режим) сохраняет признак:
	// прямая ошибка входит в неё аргументом.
	both := msgs.Errorf("hub.hostUnreachableBothWays", "vm-1", auth, "hv1", fmt.Errorf("no route to host"))
	if !sshAuthFailed(both) {
		t.Error("в комбинированной ошибке отказ учётным данным не виден")
	}
}
