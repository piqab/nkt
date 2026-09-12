package hub

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Машина, созданная на управляемом хосте, живёт в его виртуальной сети
// (libvirt NAT, 192.168.x.0/24): с самого хоста она доступна, а с хаба —
// нет и не может быть, маршрута туда не существует. Прямое подключение
// обрывается таймаутом «dial tcp 192.168.123.168:22: i/o timeout», и
// выглядит это как поломка, хотя это ровно то, ради чего NAT и делают.
//
// Решение — то же, что у ssh(1) с ProxyJump: до машины идём через её
// хост. Хаб и так держит с хостом соединение, а внутри SSH есть готовый
// способ открыть через него TCP-канал куда угодно из его сети. Рукопожатие
// с самой машиной происходит поверх этого канала — её ключом, её
// пользователем, безо всякого доверия промежуточному хосту сверх того,
// которое ему и так оказано.
//
// Альтернативы, которые этого не требуют: включить машину в мост хоста
// (тогда у неё адрес из сети хоста и она видна снаружи) или пробросить
// порт на хосте. Обе требуют работы руками при каждой машине; переход
// через хост не требует ничего и включается сам — по тому, что у записи
// машины проставлен родительский хост.

// maxJumpDepth ограничивает цепочку переходов. Машина внутри машины
// внутри хоста — уже редкость, но зацикленный parent_id в базе не должен
// заканчиваться бесконечной рекурсией.
const maxJumpDepth = 4

// sshLink — открытое соединение с хостом и, если шли через его хост,
// соединение под ним. Закрывать нужно всю цепочку: канал до машины живёт
// внутри сессии с хостом, и брошенный переход остаётся висеть.
type sshLink struct {
	client *ssh.Client
	under  *sshLink
}

// Close закрывает соединение и все переходы под ним.
func (l *sshLink) Close() error {
	if l == nil {
		return nil
	}
	err := l.client.Close()
	if l.under != nil {
		_ = l.under.Close()
	}
	return err
}

// dialHost подключается к хосту его собственными реквизитами — напрямую
// или через хост, на котором он работает.
func (m *Manager) dialHost(ctx context.Context, host store.Host) (*sshLink, error) {
	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return nil, msgs.Errorf("hub.decryptingSSHSecret", err)
	}
	return m.dialHostAs(ctx, host, host.SSHUser, host.SSHAuthKind, secret)
}

// dialHostAs — то же, но заданными реквизитами: подготовка хоста
// проверяет новый ключ и нового пользователя отдельным подключением,
// когда в записи хоста они ещё не сохранены.
func (m *Manager) dialHostAs(ctx context.Context, host store.Host, user, authKind string, secret []byte) (*sshLink, error) {
	return m.dialHostDepth(ctx, host, user, authKind, secret, 0)
}

func (m *Manager) dialHostDepth(ctx context.Context, host store.Host,
	user, authKind string, secret []byte, depth int) (*sshLink, error) {
	if isPlaceholderAddr(host.Addr) {
		return nil, msgs.Errorf("hub.addressMachineKnownYetWait", host.Name)
	}
	target := net.JoinHostPort(host.Addr, fmt.Sprintf("%d", host.SSHPort))

	// Обычный хост — напрямую, как было.
	if host.ParentID == 0 || depth >= maxJumpDepth {
		client, err := dialSSH(ctx, host.Addr, host.SSHPort, user, authKind, secret)
		if err != nil {
			return nil, err
		}
		return &sshLink{client: client}, nil
	}

	parent, err := m.db.HostByID(ctx, host.ParentID)
	if err != nil || parent.ID == host.ID {
		// Родителя нет в списке (удалён): пробуем напрямую — вдруг
		// машина всё же доступна, а отказывать заранее незачем.
		client, dialErr := dialSSH(ctx, host.Addr, host.SSHPort, user, authKind, secret)
		if dialErr != nil {
			return nil, dialErr
		}
		return &sshLink{client: client}, nil
	}

	parentSecret, err := secretbox.Decrypt(m.key, parent.SecretEnc)
	if err != nil {
		return nil, msgs.Errorf("hub.decryptingSSHSecretHost", parent.Name, err)
	}
	jump, err := m.dialHostDepth(ctx, parent, parent.SSHUser, parent.SSHAuthKind, parentSecret, depth+1)
	if err != nil {
		return nil, msgs.Errorf("hub.jumpingThroughHost", parent.Name, err)
	}

	conn, err := jump.client.DialContext(ctx, "tcp", target)
	if err != nil {
		_ = jump.Close()
		return nil, msgs.Errorf("hub.accessHost", parent.Name, target, err)
	}
	client, err := dialSSHOver(conn, target, user, authKind, secret)
	if err != nil {
		_ = jump.Close()
		return nil, err
	}
	return &sshLink{client: client, under: jump}, nil
}
