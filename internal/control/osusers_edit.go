package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Группы, правка и удаление учётных записей хоста.

// OSGroup — группа из /etc/group.
type OSGroup struct {
	Name string `json:"name"`
	GID  int    `json:"gid"`
	// System — служебная (gid < 1000): заведена пакетами.
	System bool `json:"system"`
}

var osGroupNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func keyID(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:8])
}

func nktSudoersPath(user string) string { return "/etc/sudoers.d/nkt-" + user }

// Groups — все группы хоста.
func (m *OSUserManager) Groups(context.Context) ([]OSGroup, error) {
	raw, err := m.c.ReadFile("/etc/group")
	if err != nil {
		return nil, err
	}
	out := []OSGroup{}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 3 || !osGroupNameRe.MatchString(f[0]) {
			continue
		}
		gid, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		out = append(out, OSGroup{Name: f[0], GID: gid, System: gid < osUserMinUID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// memberships — пользователь → дополнительные группы.
func (m *OSUserManager) memberships() map[string][]string {
	out := map[string][]string{}
	raw, err := m.c.ReadFile("/etc/group")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 4 {
			continue
		}
		for _, u := range strings.Split(f[3], ",") {
			if u = strings.TrimSpace(u); u != "" {
				out[u] = append(out[u], f[0])
			}
		}
	}
	for u := range out {
		sort.Strings(out[u])
	}
	return out
}

// HubUsers — под кем входит хаб: из настроек службы и из правила nkt-hub.
func (m *OSUserManager) HubUsers() map[string]bool {
	out := map[string]bool{}
	if m.hubUser != "" {
		out[m.hubUser] = true
	}
	if raw, err := m.c.ReadFile("/etc/sudoers.d/nkt-hub"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if f := strings.Fields(line); len(f) > 0 && osUserNameRe.MatchString(f[0]) {
				out[f[0]] = true
			}
		}
	}
	return out
}

// UpdateOptions — правка учётной записи; nil — не трогать.
type UpdateOptions struct {
	Groups     *[]string `json:"groups,omitempty"`
	Shell      *string   `json:"shell,omitempty"`
	Sudo       *bool     `json:"sudo,omitempty"`
	AddKeys    []string  `json:"add_keys,omitempty"`
	RemoveKeys []string  `json:"remove_keys,omitempty"`
	// ConfirmHub — да, правка задевает вход хаба (снимается его ключ,
	// sudo или группа sudo).
	ConfirmHub bool `json:"confirm_hub,omitempty"`
}

// sudoGroups — группы, членство в которых даёт sudo.
var sudoGroups = []string{"sudo", "wheel", "admin"}

func (m *OSUserManager) find(ctx context.Context, name string) (OSUser, error) {
	if !osUserNameRe.MatchString(name) {
		return OSUser{}, msgs.Errorf("control.invalidUserName", name)
	}
	list, err := m.List(ctx)
	if err != nil {
		return OSUser{}, err
	}
	for _, u := range list {
		if u.Name == name {
			return u, nil
		}
	}
	return OSUser{}, msgs.Errorf("control.osUserMissing", name)
}

// validShells — оболочки из /etc/shells и «без входа».
func (m *OSUserManager) validShells() []string {
	out := []string{"/usr/sbin/nologin"}
	if raw, err := m.c.ReadFile("/etc/shells"); err == nil {
		for _, l := range strings.Split(string(raw), "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "/") && !strings.ContainsAny(l, " '\"$`;&|") {
				out = append(out, l)
			}
		}
	}
	return out
}

// Shells — для окна.
func (m *OSUserManager) Shells() []string { return m.validShells() }

// Update — группы, оболочка, sudo nkt, ключи.
func (m *OSUserManager) Update(ctx context.Context, name string, opts UpdateOptions) error {
	u, err := m.find(ctx, name)
	if err != nil {
		return err
	}
	// Вход хаба: снять его ключ, sudo nkt или группу sudo — только с
	// явным подтверждением.
	if u.HubUser && !opts.ConfirmHub {
		touches := len(opts.RemoveKeys) > 0 || (opts.Sudo != nil && !*opts.Sudo && u.NktSudo)
		if opts.Groups != nil {
			for _, g := range sudoGroups {
				if slices.Contains(u.Groups, g) && !slices.Contains(*opts.Groups, g) {
					touches = true
				}
			}
		}
		if touches {
			return msgs.Errorf("control.osUserHubConfirm", name)
		}
	}
	if opts.Groups != nil {
		known, err := m.Groups(ctx)
		if err != nil {
			return err
		}
		var list []string
		for _, g := range *opts.Groups {
			if !slices.ContainsFunc(known, func(k OSGroup) bool { return k.Name == g }) {
				return msgs.Errorf("control.osGroupMissing", g)
			}
			if !slices.Contains(list, g) {
				list = append(list, g)
			}
		}
		if err := m.step(ctx, msgs.Tc(ctx, "control.userStepGroups"), "usermod", "-G", strings.Join(list, ","), name); err != nil {
			return err
		}
	}
	if opts.Shell != nil && *opts.Shell != u.Shell {
		if !slices.Contains(m.validShells(), *opts.Shell) {
			return msgs.Errorf("control.osShellBad", *opts.Shell)
		}
		if err := m.step(ctx, msgs.Tc(ctx, "control.userStepShell"), "usermod", "-s", *opts.Shell, name); err != nil {
			return err
		}
	}
	if opts.Sudo != nil && *opts.Sudo != u.NktSudo {
		if *opts.Sudo {
			if err := m.grantSudo(ctx, name); err != nil {
				return err
			}
		} else if err := m.step(ctx, msgs.Tc(ctx, "control.userStepSudoOff"), "rm", "-f", nktSudoersPath(name)); err != nil {
			return err
		}
	}
	for _, k := range opts.AddKeys {
		if err := m.addKey(ctx, u, k); err != nil {
			return err
		}
	}
	if len(opts.RemoveKeys) > 0 {
		if err := m.removeKeys(ctx, u, opts.RemoveKeys); err != nil {
			return err
		}
	}
	return nil
}

func (m *OSUserManager) step(ctx context.Context, what string, argv ...string) error {
	res, err := m.run(ctx, argv...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s: %s", what, strings.TrimSpace(res.Output()))
	}
	return nil
}

func (m *OSUserManager) addKey(ctx context.Context, u OSUser, key string) error {
	if _, err := ParseAuthorizedKey(key); err != nil {
		return err
	}
	home := strings.TrimSuffix(u.Home, "/")
	steps := [][]string{
		{"install", "-d", "-m", "0700", "-o", u.Name, "-g", u.Name, home + "/.ssh"},
		{"sh", "-c", fmt.Sprintf("printf '%%s\\n' %s >> %s/.ssh/authorized_keys", shellSingleQuote(strings.TrimSpace(key)), home)},
		{"sh", "-c", fmt.Sprintf("chown %[1]s:%[1]s %[2]s/.ssh/authorized_keys && chmod 0600 %[2]s/.ssh/authorized_keys", u.Name, home)},
	}
	for _, st := range steps {
		if err := m.step(ctx, msgs.Tc(ctx, "control.userStepKey"), st...); err != nil {
			return err
		}
	}
	return nil
}

var keyIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// removeKeys — убрать из authorized_keys строки с этими ID (по телу
// ключа); остальные строки остаются как были.
func (m *OSUserManager) removeKeys(ctx context.Context, u OSUser, ids []string) error {
	for _, id := range ids {
		if !keyIDRe.MatchString(id) {
			return msgs.Errorf("control.osKeyBad", id)
		}
	}
	path := strings.TrimSuffix(u.Home, "/") + "/.ssh/authorized_keys"
	raw, err := m.c.ReadFile(path)
	if err != nil {
		return err
	}
	var keep []string
	for _, line := range strings.Split(string(raw), "\n") {
		if k, err := ParseAuthorizedKey(line); err == nil && slices.Contains(ids, k.ID) {
			continue
		}
		keep = append(keep, line)
	}
	content := strings.Join(keep, "\n")
	cmd := fmt.Sprintf("t=$(mktemp) && printf '%%s' %s > \"$t\" && install -m 0600 -o %s -g %s \"$t\" %s; rc=$?; rm -f \"$t\"; exit $rc",
		shellSingleQuote(content), u.Name, u.Name, path)
	return m.step(ctx, msgs.Tc(ctx, "control.userStepKeyRemove"), "sh", "-c", cmd)
}

// Delete — удалить учётную запись (с домашним каталогом по выбору) и её
// правило nkt. Root, служебные учётки и пользователя хаба — нельзя.
func (m *OSUserManager) Delete(ctx context.Context, name string, withHome bool) error {
	u, err := m.find(ctx, name)
	if err != nil {
		return err
	}
	if u.UID == 0 || u.Name == "root" {
		return msgs.Errorf("control.osUserDeleteRoot")
	}
	if u.HubUser {
		return msgs.Errorf("control.osUserDeleteHub", name)
	}
	_ = m.step(ctx, msgs.Tc(ctx, "control.userStepSudoOff"), "rm", "-f", nktSudoersPath(name))
	argv := []string{"userdel"}
	if withHome {
		argv = append(argv, "-r")
	}
	return m.step(ctx, msgs.Tc(ctx, "control.userStepDelete"), append(argv, name)...)
}
