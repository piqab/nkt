package hub

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// miniSSHD — SSH-сервер в процессе: принимает соединения без проверки,
// отвечает на глобальные запросы (как sshd на keepalive@openssh.com —
// отказом). close() рвёт все принятые соединения.
type miniSSHD struct {
	ln    net.Listener
	conns chan net.Conn
}

func startMiniSSHD(t *testing.T) *miniSSHD {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &miniSSHD{ln: ln, conns: make(chan net.Conn, 16)}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			d.conns <- c
			go func(c net.Conn) {
				_, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go func() {
					for r := range reqs {
						_ = r.Reply(false, nil)
					}
				}()
				for ch := range chans {
					_ = ch.Reject(ssh.Prohibited, "")
				}
			}(c)
		}
	}()
	return d
}

func (d *miniSSHD) dial(t *testing.T) *ssh.Client {
	t.Helper()
	c, err := ssh.Dial("tcp", d.ln.Addr().String(), &ssh.ClientConfig{User: "u", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Ошибка одного запроса не закрывает живое общее соединение; мёртвое —
// закрывается и уходит из пула; уже заменённое свежим — не трогается.
func TestFailClientKeepsLiveConnection(t *testing.T) {
	old := clientKeepaliveTimeout
	clientKeepaliveTimeout = 2 * time.Second
	t.Cleanup(func() { clientKeepaliveTimeout = old })
	m, _ := newTestManager(t)
	d := startMiniSSHD(t)

	live := d.dial(t)
	m.conns[1] = &hostConn{link: &sshLink{client: live}, lastUsed: time.Now()}
	m.failClient(1, live)
	if _, ok := m.conns[1]; !ok {
		t.Fatal("живое соединение закрыто из-за ошибки одного запроса")
	}

	// Сервер рвёт соединение — keepalive не проходит, соединение уходит.
	(<-d.conns).Close()
	time.Sleep(100 * time.Millisecond)
	m.failClient(1, live)
	if _, ok := m.conns[1]; ok {
		t.Fatal("мёртвое соединение осталось в пуле")
	}

	// В пуле уже свежее — неудача старого его не трогает.
	fresh := d.dial(t)
	<-d.conns
	m.conns[1] = &hostConn{link: &sshLink{client: fresh}, lastUsed: time.Now()}
	m.failClient(1, live)
	if hc, ok := m.conns[1]; !ok || hc.link.client != fresh {
		t.Fatal("свежее соединение выброшено из-за неудачи старого")
	}
	_ = fresh.Close()
}

// «Недоступен» — со второй неудачи подряд: одна неудача (нагруженный хост,
// первая минута после перезапуска хаба) состояние не меняет.
func TestUnreachableAfterTwoFails(t *testing.T) {
	m, db := newTestManager(t)
	ctx := t.Context()
	id, err := db.CreateHost(ctx, "h1", "127.0.0.1", 22, "root", "password", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	m.overview[id] = hostOverview{reachable: true}
	m.recordUnreachable(ctx, id, errTest("timeout"))
	if ov := m.overview[id]; !ov.reachable || ov.errMsg != "timeout" {
		t.Fatalf("после одной неудачи: %+v", ov)
	}
	m.recordUnreachable(ctx, id, errTest("timeout"))
	if m.overview[id].reachable {
		t.Fatal("после двух неудач подряд хост всё ещё доступен")
	}
	// Без прошлых сведений (только что перезапущенный хаб) — тоже не с
	// первой неудачи.
	delete(m.overview, id)
	delete(m.pollFails, id)
	m.recordUnreachable(ctx, id, errTest("eof"))
	if _, ok := m.overview[id]; ok {
		t.Fatal("после первой неудачи у нового хоста уже есть «недоступен»")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
