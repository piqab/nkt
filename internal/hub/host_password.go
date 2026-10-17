package hub

import (
	"context"
	"fmt"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Пароль SSH нового хоста в базу не пишется: первая установка переводит
// хост на ключ хаба и выключает вход по паролю, так что пароль нужен ей
// одной. До этого он живёт в памяти хаба (oneTime, hostSecretTTL); в
// записи хоста — пустой секрет. Перезапуск хаба или истёкший срок — пароль
// вводится заново в «Изменить».

// AddHostPasswordInMemory — AddHost для входа по паролю без записи пароля.
func (m *Manager) AddHostPasswordInMemory(ctx context.Context, name, addr string, sshPort int, sshUser, password string, terminalEnabled bool) (int64, error) {
	if name == "" || addr == "" || sshUser == "" || password == "" {
		return 0, msgs.Errorf("hub.specifyNameAddressSSHUser")
	}
	if sshPort == 0 {
		sshPort = 22
	}
	empty, err := secretbox.Encrypt(m.key, nil)
	if err != nil {
		return 0, msgs.Errorf("hub.encryptingSecret", err)
	}
	id, err := m.db.CreateHost(ctx, name, addr, sshPort, sshUser, store.HostAuthPassword, empty)
	if err != nil {
		return 0, err
	}
	oneTime.Set(sshPasswordKey(id), []byte(password), hostSecretTTL)
	if err := m.db.SetHostTerminalEnabled(ctx, id, terminalEnabled); err != nil {
		return 0, err
	}
	return id, nil
}

// keepPasswordInMemory — новый пароль ещё не установленного хоста: в
// память, а в базе секрет очищается.
func (m *Manager) keepPasswordInMemory(ctx context.Context, hostID int64, password string) error {
	empty, err := secretbox.Encrypt(m.key, nil)
	if err != nil {
		return msgs.Errorf("hub.encryptingSecret", err)
	}
	if err := m.db.SetHostSecret(ctx, hostID, store.HostAuthPassword, empty); err != nil {
		return err
	}
	oneTime.Set(sshPasswordKey(hostID), []byte(password), hostSecretTTL)
	return nil
}

// hostSecret — секрет подключения хоста: из базы, а пароль, который хаб
// не хранит, — из памяти.
func (m *Manager) hostSecret(host store.Host) ([]byte, error) {
	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		return nil, msgs.Errorf("hub.decryptingSSHSecret", err)
	}
	if len(secret) == 0 && host.SSHAuthKind == store.HostAuthPassword {
		if pw, ok := oneTime.Get(sshPasswordKey(host.ID)); ok {
			return pw, nil
		}
		return nil, msgs.Errorf("hub.sshPasswordNotKept", host.Name)
	}
	return secret, nil
}

// PlaceKeyWithPassword — ключ хаба в authorized_keys пользователя хоста по
// одноразовому паролю SSH, затем проверка входа ключом.
func (m *Manager) PlaceKeyWithPassword(ctx context.Context, hostID int64, password []byte) error {
	defer wipe(password)
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return err
	}
	if host.SSHAuthKind != store.HostAuthKey {
		return msgs.Errorf("hub.placeKeyNotKeyHost", host.Name)
	}
	line, err := m.PublicKeyLine(ctx, hostID)
	if err != nil {
		return err
	}
	link, err := m.dialHostAs(ctx, host, host.SSHUser, store.HostAuthPassword, password)
	if err != nil {
		return msgs.Errorf("hub.placeKeyLogin", host.SSHUser, err)
	}
	defer link.Close()
	k := shellQuote(line)
	cmd := fmt.Sprintf("umask 077; mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys"+
		" && { grep -qxF %[1]s ~/.ssh/authorized_keys || printf '%%s\\n' %[1]s >> ~/.ssh/authorized_keys; }", k)
	if out, err := runRemote(link.client, cmd); err != nil {
		return msgs.Errorf("hub.installingKey", host.SSHUser, err, lastLines(out, 3))
	}
	check, err := m.dialHost(ctx, host)
	if err != nil {
		return msgs.Errorf("hub.keyLoginAsDoesWork", host.SSHUser, err)
	}
	_ = check.Close()
	if host.SSHUser != "root" && sudoPasswordWorks(link.client, password) {
		oneTime.Set(sudoPasswordKey(hostID), password, hostSecretTTL)
	}
	return nil
}
