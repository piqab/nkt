package hub

import (
	"bytes"
	"crypto/ed25519"
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
	out, err := runRemote(client, "cat "+hubsudo.PubKeyPath+" 2>/dev/null; true")
	return err == nil && strings.TrimSpace(out) == strings.TrimSpace(hubsudo.PublicText(m.signKey()))
}

var staleSerialRe = regexp.MustCompile(`stale serial, last (\d+)`)

// hubSudo — подписанная операция на хосте. root сам себе hub-sudo не
// нужен, но тот же путь годится и для него (sudo не вызывается).
func (m *Manager) hubSudo(client *ssh.Client, sshUser string, req hubsudo.Request) (string, error) {
	cmd := hubsudo.Command
	if sshUser != "root" {
		cmd = "sudo -n " + cmd
	}
	var floor int64
	for attempt := 0; attempt < 2; attempt++ {
		req.Serial = nextSerial(floor)
		env, err := hubsudo.Sign(m.signKey(), req)
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
	pub := hubsudo.PublicText(m.signKey())
	rule := hubsudo.SudoersRule(sshUser)
	script := "set -e; umask 022; install -d -m 755 /etc/nkt; " +
		"printf %s " + shellQuote(pub) + " > " + hubsudo.PubKeyPath + ".new; chmod 644 " + hubsudo.PubKeyPath + ".new; mv " + hubsudo.PubKeyPath + ".new " + hubsudo.PubKeyPath + "; " +
		"t=$(mktemp); printf %s " + shellQuote(rule) + " > $t; visudo -cf $t >/dev/null; chmod 440 $t; mv $t " + sudoersDropIn
	out, err := runRemote(client, "sudo -n sh -c "+shellQuote(script))
	if err != nil {
		return "", diagnoseInstallError(sshUser, sudoersDropIn, err, out)
	}
	if _, err := runRemote(client, "sudo -n true"); err == nil {
		return "full", nil
	}
	return "narrow", nil
}
