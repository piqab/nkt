package hub

import (
	"context"

	"bytes"
	"crypto/ed25519"
	"github.com/piqab/nkt/internal/store"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/hubsudo"
	"github.com/piqab/nkt/internal/msgs"
)

// Узкий sudo (см. internal/hubsudo): на хосте лежит открытый ключ хаба и
// правило «без пароля — только nkt hub-sudo». Хаб шлёт подписанный запрос
// через stdin; всё остальное, что раньше шло `sudo -n …`, на таком хосте
// без пароля не выполнить.

var (
	sudoSerialMu   sync.Mutex
	sudoLastSerial int64
)

// signKey — ключ подписи хаба (из мастер-ключа).
func (m *Manager) signKey() ed25519.PrivateKey { return hubsudo.KeyFromSecret(m.key) }

// nextSerial — номер запроса: время хаба в наносекундах, строго растущее
// в пределах процесса.
func nextSerial(atLeast int64) int64 {
	sudoSerialMu.Lock()
	defer sudoSerialMu.Unlock()
	s := time.Now().UnixNano()
	if s <= sudoLastSerial {
		s = sudoLastSerial + 1
	}
	if s <= atLeast {
		s = atLeast + 1
	}
	sudoLastSerial = s
	return s
}

// hasHubKey — на хосте наш открытый ключ: хост переведён на узкий sudo
// этим хабом.
func (m *Manager) hasHubKey(client *ssh.Client) bool {
	_, _, ok := m.keyFor(client)
	return ok
}

var staleSerialRe = regexp.MustCompile(`stale serial, last (\d+)`)

// hubSudo — подписанная операция на хосте. root сам себе hub-sudo не
// нужен, но тот же путь годится и для него (sudo не вызывается).
func (m *Manager) hubSudo(client *ssh.Client, sshUser string, req hubsudo.Request) (string, error) {
	cmd := hubsudo.Command
	if sshUser != "root" {
		cmd = "sudo -n " + cmd
	}
	// Подпись — тем ключом, которому хост доверяет (свой или прежнего
	// хаба после переезда, см. hubsudo_legacy.go).
	key, _, _ := m.keyFor(client)
	var floor int64
	for attempt := 0; attempt < 2; attempt++ {
		req.Serial = nextSerial(floor)
		env, err := hubsudo.Sign(key, req)
		if err != nil {
			return "", err
		}
		out, err := runRemoteStdin(client, cmd, env)
		if err == nil {
			return out, nil
		}
		// Часы хаба ушли назад (или другой процесс хаба) — ещё раз с
		// номером больше принятого хостом.
		if mm := staleSerialRe.FindStringSubmatch(out); mm != nil && attempt == 0 {
			floor, _ = strconv.ParseInt(mm[1], 10, 64)
			continue
		}
		return out, diagnoseInstallError(sshUser, "nkt hub-sudo "+req.Op, err, out)
	}
	return "", msgs.Errorf("hub.hubSudoSerial")
}

// runRemoteStdin — команда с данными на stdin (конверт hub-sudo: в
// командную строку и журналы он не попадает).
func runRemoteStdin(client *ssh.Client, cmd string, stdin []byte) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", msgs.Errorf("hub.openingSSHSession", err)
	}
	defer session.Close()
	session.Stdin = bytes.NewReader(stdin)
	out, err := session.CombinedOutput("export LC_ALL=C; " + cmd)
	return string(out), err
}

// narrowSudo — перевести хост на узкий sudo, пока полный ещё действует:
// открытый ключ хаба в /etc/nkt (root, 0644) и правило sudoers только на
// hub-sudo (проверка visudo до замены — неверный файл сломал бы sudo
// целиком). Итог: узкий — `sudo -n true` теперь просит пароль; полный по
// чужому правилу — остался «без пароля».
func (m *Manager) narrowSudo(client *ssh.Client, sshUser string) (string, error) {
	if sshUser == "root" || !validAdminUser.MatchString(sshUser) {
		return "", msgs.Errorf("hub.narrowSudoUser", sshUser)
	}
	// Ключ прежнего хаба (переезд) — сменить на свой, если хост умеет.
	m.rekeyIfLegacy(client, sshUser)
	// Уже сужен (ключ хаба на месте, hub-sudo без пароля, полного sudo
	// нет) — делать нечего; полный sudo для повтора не нужен.
	if m.hasHubKey(client) {
		if st, err := m.probeSudo(client, sshUser); err == nil && st.Status == store.SudoStatusNarrow {
			if out, err := m.hubSudo(client, sshUser, hubsudo.Request{Op: hubsudo.OpPing}); err == nil && strings.Contains(out, "ok") {
				return "already", nil
			}
		}
	}
	pub := hubsudo.PublicText(m.signKey())
	rule := hubsudo.SudoersRule(sshUser)
	keyScript := "set -e; umask 022; install -d -m 755 /etc/nkt; " +
		"printf %s " + shellQuote(pub) + " > " + hubsudo.PubKeyPath + ".new; chmod 644 " + hubsudo.PubKeyPath + ".new; mv " + hubsudo.PubKeyPath + ".new " + hubsudo.PubKeyPath
	if out, err := runRemote(client, "sudo -n sh -c "+shellQuote(keyScript)); err != nil {
		return "", diagnoseInstallError(sshUser, hubsudo.PubKeyPath, err, out)
	}
	// Пока полный sudo действует — убедиться, что hub-sudo на хосте есть и
	// принимает ключ: старый nkt такой команды не знает, и с узким
	// правилом хаб не смог бы его даже обновить.
	if out, err := m.hubSudo(client, sshUser, hubsudo.Request{Op: hubsudo.OpPing}); err != nil || !strings.Contains(out, "ok") {
		return "", msgs.Errorf("hub.narrowSudoNeedsUpdate", strings.TrimSpace(out))
	}
	ruleScript := "set -e; t=$(mktemp); printf %s " + shellQuote(rule) + " > $t; visudo -cf $t >/dev/null; chmod 440 $t; mv $t " + sudoersDropIn
	if out, err := runRemote(client, "sudo -n sh -c "+shellQuote(ruleScript)); err != nil {
		return "", diagnoseInstallError(sshUser, sudoersDropIn, err, out)
	}
	if _, err := runRemote(client, "sudo -n true"); err == nil {
		return "full", nil
	}
	return "narrow", nil
}

// NarrowSudo — «сузить sudo» уже установленного хоста с полным sudo.
func (m *Manager) NarrowSudo(ctx context.Context, hostID int64) (string, error) {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return "", err
	}
	if host.SSHUser == "root" {
		return "", msgs.Errorf("hub.hostConnectedAsRootSudo")
	}
	link, err := m.dialHost(ctx, host)
	if err != nil {
		return "", err
	}
	defer link.Close()
	mode, err := m.narrowSudo(link.client, host.SSHUser)
	if err != nil {
		return "", err
	}
	fallback := store.SudoStatusNarrow
	if mode == "full" {
		fallback = store.SudoStatusNopasswd
	}
	// Итог — по живой проверке: окно пишет «полный sudo остался» ровно
	// тогда, когда значок останется красным.
	if m.refreshSudoStatus(ctx, hostID, link.client, host.SSHUser, fallback) == store.SudoStatusNopasswd {
		mode = "full"
	}
	return mode, nil
}

// SudoInfo — что разрешено хабу без пароля на хосте (окно «что
// разрешено»): операции hub-sudo и правило sudoers.
func SudoInfo(sshUser string) map[string]any {
	return map[string]any{"ops": hubsudo.Ops, "rule": hubsudo.SudoersRule(sshUser), "rule_path": sudoersDropIn,
		"key_path": hubsudo.PubKeyPath, "command": hubsudo.Command}
}
