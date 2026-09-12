package hub

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net/url"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Удаление хоста из хаба всегда убирало только запись в базе — на самом
// сервере оставались служба, бинарник, данные, правило sudo и ключ хаба в
// authorized_keys. Для чужого или сдаваемого сервера это ровно та работа,
// которую потом делают руками, вспоминая по частям, что именно nkt после
// себя оставил.
//
// Каждый шаг здесь — отдельная галочка в форме удаления, потому что цена
// у них разная: снести службу и вернуться к прежнему состоянию сервера —
// одно, а стереть историю правок конфигов или удалить учётную запись
// вместе с домашним каталогом — совсем другое.

// PurgeOptions описывает, что убрать с хоста при удалении.
type PurgeOptions struct {
	// Service — служба, бинарник и /etc/netknownsthat.
	Service bool `json:"service"`
	// Data — /var/lib/netknownsthat (база, история версий конфигов,
	// архивы образов) и /var/log/netknownsthat.
	Data bool `json:"data"`
	// Access — правило в /etc/sudoers.d и ключ хаба в authorized_keys.
	Access bool `json:"access"`
	// User — учётная запись, заведённая автонастройкой.
	User bool `json:"user"`
	// RestorePassword возвращает вход по паролю, убирая drop-in, которым
	// его выключила автонастройка. Включено по умолчанию: вместе с хабом
	// с хоста уезжает и его ключ, и без пароля хост остался бы без
	// единого способа входа — то есть потерянным.
	RestorePassword bool `json:"restore_password"`
	// VM удаляет саму машину на хосте, где она создана: домен и его
	// диски. Только для машин — у обычного хоста нет хозяина, который мог
	// бы его выключить.
	//
	// Делается не по SSH в саму машину, а через её хост: он ею и
	// управляет, а её собственный доступ для этого не нужен и может быть
	// уже недоступен (адрес не определился, машина не поднялась).
	VM bool `json:"vm"`
	// VMDisks удаляет вместе с машиной и её диски. Отдельный вопрос:
	// описание домена заводится заново за минуту, а диск с данными — нет,
	// и молча стирать его вместе с записью нельзя.
	VMDisks bool `json:"vm_disks"`
}

// Any сообщает, есть ли что делать на хосте вообще.
func (o PurgeOptions) Any() bool {
	return o.Service || o.Data || o.Access || o.User || o.RestorePassword || o.VM
}

// onHost сообщает, нужно ли заходить в сам хост по SSH.
func (o PurgeOptions) onHost() bool {
	return o.Service || o.Data || o.Access || o.User || o.RestorePassword
}

// PurgeResult — что удалось сделать. Ошибка подключения не мешает убрать
// хост из хаба: сервер может быть уже погашен, и тогда единственный
// разумный исход — стереть запись и честно сказать, что на хосте ничего
// не тронуто.
type PurgeResult struct {
	Attempted bool     `json:"attempted"`
	OK        bool     `json:"ok"`
	Steps     []string `json:"steps,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// PurgeHost убирает с хоста то, что перечислено в opts.
func (m *Manager) PurgeHost(ctx context.Context, hostID int64, opts PurgeOptions) PurgeResult {
	res := PurgeResult{Attempted: true}
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	// Машина удаляется целиком — тогда чистить внутри неё уже нечего, и
	// её собственный SSH не нужен вовсе.
	if opts.VM {
		if host.ParentID == 0 {
			res.Error = msgs.Tc(ctx, "hub.purgeNotMachine")
			return res
		}
		steps, err := m.deleteHostVM(ctx, host, opts.VMDisks)
		res.Steps = append(res.Steps, steps...)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.OK = true
		return res
	}
	if !opts.onHost() {
		res.OK = true
		return res
	}

	secret, err := secretbox.Decrypt(m.key, host.SecretEnc)
	if err != nil {
		res.Error = msgs.Tc(ctx, "hub.decryptingSSHSecret2", err)
		return res
	}
	link, err := m.dialHost(ctx, host)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer link.Close()
	client := link.client

	sudo := sudoPrefix(host.SSHUser)
	step := func(name, cmd string) {
		if out, err := runRemote(client, cmd); err != nil {
			res.Steps = append(res.Steps, msgs.Tc(ctx, "hub.failed", name, lastLines(out, 2)))
			return
		}
		res.Steps = append(res.Steps, msgs.Tc(ctx, "hub.stepDone", name))
	}

	// Первым шагом, до всего остального: если дальше что-то оборвётся,
	// вход по паролю уже вернулся, и хост не останется недоступным.
	if opts.RestorePassword {
		step(msgs.Tc(ctx, "hub.purgePasswordRestored"), restorePasswordCmd(sudo))
	}

	if opts.Service {
		// disable --now до удаления файла юнита: после удаления systemd уже
		// не знает, что останавливать, и процесс продолжит работать до
		// перезагрузки.
		step(msgs.Tc(ctx, "hub.purgeServiceStopped"),
			sudo+"systemctl disable --now netknownsthat 2>/dev/null; true")
		step(msgs.Tc(ctx, "hub.purgeUnitRemoved"),
			sudo+"rm -f "+remoteServicePath+" "+remoteBinPath+" && "+sudo+"systemctl daemon-reload")
		step(msgs.Tc(ctx, "hub.purgeConfigRemoved"), sudo+"rm -rf /etc/netknownsthat")
	}
	if opts.Data {
		step(msgs.Tc(ctx, "hub.purgeDataRemoved"), sudo+"rm -rf /var/lib/netknownsthat /var/log/netknownsthat")
	}
	if opts.Access {
		step(msgs.Tc(ctx, "hub.purgeSudoRemoved"), sudo+"rm -f "+sudoersDropIn)
		if line := hubPublicKeyLine(host, secret); line != "" {
			step(msgs.Tc(ctx, "hub.purgeKeyRemoved"), removeAuthorizedKeyCmd(sudo, host.SSHUser, line))
		}
	}
	if opts.User && host.SSHUser != "root" {
		// Последним шагом: под этим пользователем открыто текущее
		// соединение, и всё, что делается после, работать уже не обязано.
		step(msgs.Tc(ctx, "hub.purgeAccountRemoved", host.SSHUser),
			sudo+"userdel -r "+host.SSHUser+" 2>&1; true")
	}

	res.OK = true
	return res
}

// hubPublicKeyLine — строка authorized_keys, соответствующая ключу, под
// которым хаб ходит на хост. Для парольного доступа возвращает пустую
// строку: ключа хаб туда не клал, и удалять нечего.
func hubPublicKeyLine(host store.Host, secret []byte) string {
	if host.SSHAuthKind != store.HostAuthKey {
		return ""
	}
	signer, err := ssh.ParsePrivateKey(secret)
	if err != nil {
		return ""
	}
	return formatAuthorizedKey(signer.PublicKey())
}

// removeAuthorizedKeyCmd вычёркивает ровно одну строку — ту, что положил
// сам хаб, сравнивая по телу ключа. Остальные строки файла остаются как
// были: там почти наверняка чей-то рабочий доступ.
func removeAuthorizedKeyCmd(sudo, user, keyLine string) string {
	home := "/root"
	if user != "root" {
		home = "/home/" + user
	}
	path := home + "/.ssh/authorized_keys"
	// Сравнение по телу ключа, а не по всей строке: комментарий мог быть
	// изменён на месте, а ключ от этого другим не становится.
	body := keyLine
	if fields := strings.Fields(keyLine); len(fields) >= 2 {
		body = fields[1]
	}
	return fmt.Sprintf(
		"test -f %[1]s && %[2]sgrep -vF %[3]s %[1]s > /tmp/nkt-ak && %[2]sinstall -m 0600 -o %[4]s -g %[4]s /tmp/nkt-ak %[1]s;"+
			" rc=$?; rm -f /tmp/nkt-ak; exit $rc",
		path, sudo, shellQuote(body), user)
}

// restorePasswordCmd убирает drop-in, которым автонастройка выключала
// вход по паролю, и просит sshd перечитать конфигурацию.
//
// Убирается только свой файл: чужие настройки sshd не трогаются, даже
// если пароль выключен где-то ещё. Перед перезагрузкой конфигурация
// проверяется sshd -t — оставить демон с конфигурацией, которую он не
// принимает, в момент удаления хаба означало бы отрезать хост совсем.
// reload, а не restart: перезапуск оборвал бы то самое соединение, по
// которому идёт удаление.
func restorePasswordCmd(sudo string) string {
	return fmt.Sprintf(
		"if [ -f %[1]s ]; then %[2]srm -f %[1]s && %[2]ssshd -t"+
			" && { %[2]ssystemctl reload ssh 2>/dev/null || %[2]ssystemctl reload sshd; };"+
			" else echo 'drop-in absent - password login was never disabled'; fi",
		nktSSHDropIn, sudo)
}

// deleteHostVM убирает машину на хосте, где она создана: гасит домен и
// удаляет его вместе с дисками.
//
// Через API хоста, а не по SSH в саму машину: удалять машину изнутри
// неё — то же, что пилить сук; да и доступа туда может не быть вовсе.
// Имя домена — имя записи: под ним машина и заводилась.
func (m *Manager) deleteHostVM(ctx context.Context, host store.Host, removeDisks bool) ([]string, error) {
	parent, err := m.db.HostByID(ctx, host.ParentID)
	if err != nil {
		return nil, msgs.Errorf("hub.machineSHostFound", err)
	}
	var steps []string

	// Сверяемся со списком: если домена там нет, всё остальное только
	// запутает — «machine not found» вместо «его уже нет».
	var list struct {
		VMs []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"vms"`
	}
	if _, err := m.HostAPI(ctx, parent.ID, "GET", "/api/vms", nil, &list); err != nil {
		return steps, msgs.Errorf("hub.listingMachinesHost", parent.Name, err)
	}
	found := false
	for _, vm := range list.VMs {
		if vm.Name == host.Name {
			found = true
			break
		}
	}
	if !found {
		steps = append(steps, msgs.Tc(ctx, "hub.machineLongerExistsHost", host.Name, parent.Name))
		return steps, nil
	}

	// force: запущенную машину хост гасит сам, перед удалением. Просить
	// об этом отдельным вызовом нельзя — состояние доменов хост держит в
	// снимке инвентаря, и сразу после выключения там всё ещё «running».
	path := "/api/vms/" + url.PathEscape(host.Name) + "?force=true"
	if removeDisks {
		path += "&remove_storage=true"
	}
	if _, err := m.HostAPI(ctx, parent.ID, "DELETE", path, nil, nil); err != nil {
		return steps, msgs.Errorf("hub.removingMachineHost", host.Name, parent.Name, err)
	}
	if removeDisks {
		steps = append(steps, msgs.Tc(ctx, "hub.machinePoweredOffDisksRemoved", host.Name, parent.Name))
	} else {
		steps = append(steps, msgs.Tc(ctx, "hub.machinePoweredOffRemovedHost", host.Name, parent.Name))
	}
	return steps, nil
}
