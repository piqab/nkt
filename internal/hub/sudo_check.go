package hub

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/hubsudo"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Живая проверка sudo пользователя хаба на хосте: что на самом деле
// разрешено без пароля, а не то, что хаб запомнил при установке (файл
// sudoers могли убрать или добавить руками).

// SudoSource — строка sudoers, дающая пользователю sudo без пароля на всё.
type SudoSource struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Text string `json:"text"`
	// Kind — user (пользователь назван прямо), group (%группа), all (ALL),
	// alias (через User_Alias), nkt (правило самого nkt).
	Kind string `json:"kind"`
	// Removable — хаб может отключить строку сам: отдельный файл в
	// sudoers.d, где назван только этот пользователь, и хост уже сужен.
	Removable bool `json:"removable"`
}

// SudoState — итог проверки.
type SudoState struct {
	Status string `json:"status"`
	// HubKey — на хосте ключ этого хаба; NarrowRule — есть узкое правило
	// hub-sudo; Full — sudo без пароля на всё.
	HubKey     bool `json:"hub_key"`
	NarrowRule bool `json:"narrow_rule"`
	Full       bool `json:"full"`
	// FullRules — строки `sudo -l`, дающие всё без пароля.
	FullRules []string     `json:"full_rules,omitempty"`
	Sources   []SudoSource `json:"sources,omitempty"`
	// Password — passwd -S: P — пароль задан, L — заблокирован, NP — нет.
	Password string `json:"password,omitempty"`
}

// parseSudoList — разбор `sudo -n -l`: есть ли «NOPASSWD: … ALL» и узкое
// правило hub-sudo.
func parseSudoList(out string) (full bool, fullRules []string, narrow bool) {
	_, body, ok := strings.Cut(out, "may run the following commands")
	if !ok {
		return false, nil, false
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, "NOPASSWD:")
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len("NOPASSWD:"):])
		// Другие метки (SETENV:, NOEXEC: …) перед командами.
		for {
			f := strings.Fields(rest)
			if len(f) == 0 || !sudoTagRe.MatchString(f[0]) {
				break
			}
			rest = strings.TrimSpace(strings.TrimPrefix(rest, f[0]))
		}
		for _, c := range strings.Split(rest, ",") {
			c = strings.TrimSpace(c)
			if c == "ALL" {
				full = true
				fullRules = append(fullRules, line)
				break
			}
			if c == hubsudo.Command {
				narrow = true
			}
		}
	}
	return full, fullRules, narrow
}

var sudoTagRe = regexp.MustCompile(`^[A-Z_]+:$`)

// classifySudoLine — строка sudoers с «NOPASSWD: … ALL» для user (с
// группами groups): вид или "" — не про него / не на всё.
func classifySudoLine(text, user string, groups []string) string {
	line := strings.TrimSpace(text)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "@") || strings.HasPrefix(line, "Defaults") ||
		strings.Contains(line, "_Alias") || !strings.Contains(line, "NOPASSWD:") {
		return ""
	}
	full, _, _ := parseSudoList("may run the following commands\n" + line[strings.Index(line, "NOPASSWD:"):])
	if !full {
		return ""
	}
	who := strings.Fields(line)[0]
	kind := ""
	for _, w := range strings.Split(who, ",") {
		switch {
		case w == user:
			return "user"
		case strings.HasPrefix(w, "%") && slices.Contains(groups, strings.TrimPrefix(w, "%")):
			kind = "group"
		case w == "ALL":
			kind = "all"
		case kind == "" && sudoAliasRe.MatchString(w):
			kind = "alias"
		}
	}
	return kind
}

var sudoAliasRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

var sudoersDFileRe = regexp.MustCompile(`^/etc/sudoers\.d/[A-Za-z0-9_-]{1,64}$`)

// probeSudo — проверка по уже открытому соединению.
func (m *Manager) probeSudo(client *ssh.Client, user string) (SudoState, error) {
	if user == "root" {
		return SudoState{Status: store.SudoStatusRoot}, nil
	}
	st := SudoState{HubKey: m.hasHubKey(client)}
	if out, err := runRemote(client, "passwd -S 2>/dev/null"); err == nil {
		if f := strings.Fields(out); len(f) > 1 {
			st.Password = f[1]
		}
	}
	out, err := runRemote(client, "sudo -n -l 2>&1")
	if err != nil {
		if strings.Contains(out, "password is required") || strings.Contains(out, "may not run sudo") || strings.Contains(out, "not in the sudoers") {
			st.Status = store.SudoStatusPasswordRequired
			return st, nil
		}
		return st, msgs.Errorf("hub.sudoCheckFailed", strings.TrimSpace(out))
	}
	st.Full, st.FullRules, st.NarrowRule = parseSudoList(out)
	switch {
	case st.Full:
		st.Status = store.SudoStatusNopasswd
	case st.NarrowRule && st.HubKey:
		st.Status = store.SudoStatusNarrow
	default:
		st.Status = store.SudoStatusPasswordRequired
	}
	if st.Full {
		st.Sources = m.sudoSources(client, user, st.HubKey && st.NarrowRule)
	}
	return st, nil
}

// sudoSources — откуда полный sudo (читается полным же sudo, пока он есть).
func (m *Manager) sudoSources(client *ssh.Client, user string, narrowed bool) []SudoSource {
	gout, _ := runRemote(client, "id -Gn")
	groups := strings.Fields(gout)
	out, err := runRemote(client, "sudo -n grep -rHn -- '' /etc/sudoers /etc/sudoers.d")
	if err != nil {
		return nil
	}
	var res []SudoSource
	for _, l := range strings.Split(out, "\n") {
		file, rest, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		num, text, ok := strings.Cut(rest, ":")
		n, err := strconv.Atoi(num)
		if !ok || err != nil {
			continue
		}
		kind := classifySudoLine(text, user, groups)
		if kind == "" {
			continue
		}
		if file == sudoersDropIn {
			kind = "nkt"
		}
		res = append(res, SudoSource{File: file, Line: n, Text: strings.TrimSpace(text), Kind: kind,
			Removable: narrowed && kind == "user" && sudoersDFileRe.MatchString(file) && file != sudoersDropIn &&
				strings.Fields(strings.TrimSpace(text))[0] == user})
	}
	return res
}

// CheckSudo — живая проверка sudo хоста; состояние записывается в хаб.
func (m *Manager) CheckSudo(ctx context.Context, hostID int64) (SudoState, error) {
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return SudoState{}, err
	}
	if host.SSHUser == "root" {
		return SudoState{Status: store.SudoStatusRoot}, nil
	}
	link, err := m.dialHost(ctx, host)
	if err != nil {
		return SudoState{Status: host.SudoStatus}, err
	}
	defer link.Close()
	st, err := m.probeSudo(link.client, host.SSHUser)
	if err != nil {
		st.Status = host.SudoStatus
		return st, err
	}
	if st.Status != host.SudoStatus {
		_ = m.db.SetHostSudoStatus(ctx, hostID, st.Status)
	}
	return st, nil
}

// DisableSudoRule — закомментировать чужую строку sudoers, дающую
// пользователю хаба полный sudo без пароля. Только отдельный файл в
// sudoers.d, где назван один этот пользователь, и только на уже суженном
// хосте; строка сверяется с той, что видел оператор; файл проверяется
// visudo до замены, прежний остаётся рядом (.nkt-bak — sudo такие файлы
// не читает).
func (m *Manager) DisableSudoRule(ctx context.Context, hostID int64, file string, line int, text string) (SudoState, error) {
	if !sudoersDFileRe.MatchString(file) || file == sudoersDropIn {
		return SudoState{}, msgs.Errorf("hub.sudoRuleFileBad", file)
	}
	host, err := m.db.HostByID(ctx, hostID)
	if err != nil {
		return SudoState{}, err
	}
	link, err := m.dialHost(ctx, host)
	if err != nil {
		return SudoState{}, err
	}
	defer link.Close()
	st, err := m.probeSudo(link.client, host.SSHUser)
	if err != nil {
		return st, err
	}
	var src *SudoSource
	for i := range st.Sources {
		if s := st.Sources[i]; s.File == file && s.Line == line && s.Text == strings.TrimSpace(text) {
			src = &st.Sources[i]
		}
	}
	if src == nil {
		return st, msgs.Errorf("hub.sudoRuleChanged", file, line)
	}
	if !src.Removable {
		return st, msgs.Errorf("hub.sudoRuleNotRemovable", file)
	}
	raw, err := runRemote(link.client, "sudo -n cat "+shellQuote(file))
	if err != nil {
		return st, diagnoseInstallError(host.SSHUser, file, err, raw)
	}
	lines := strings.Split(raw, "\n")
	if line < 1 || line > len(lines) || strings.TrimSpace(lines[line-1]) != src.Text {
		return st, msgs.Errorf("hub.sudoRuleChanged", file, line)
	}
	lines[line-1] = "# nkt: отключено хабом " + time.Now().Format("2006-01-02") + ": " + lines[line-1]
	q := shellQuote(file)
	script := "set -e; t=$(mktemp); cat > \"$t\"; visudo -cf \"$t\" >/dev/null; cp -p " + q + " " + q + ".nkt-bak; chown root:root \"$t\"; chmod 440 \"$t\"; mv \"$t\" " + q
	if out, err := runRemoteStdin(link.client, "sudo -n sh -c "+shellQuote(script), []byte(strings.Join(lines, "\n"))); err != nil {
		return st, diagnoseInstallError(host.SSHUser, file, err, out)
	}
	st, err = m.probeSudo(link.client, host.SSHUser)
	if err == nil {
		_ = m.db.SetHostSudoStatus(ctx, hostID, st.Status)
	}
	return st, err
}
