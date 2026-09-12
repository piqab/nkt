package files

import (
	"context"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"net/url"
	"os"
	gopath "path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/vmcreate"
)

// git clone — заданием с журналом: большой репозиторий тянется минутами и
// должен переживать закрытую вкладку.
//
// Приватные репозитории — двумя способами, и ни один не оставляет секрет
// на диске в открытом виде:
//
//   - HTTPS с токеном или паролем: секрет уходит git через GIT_ASKPASS —
//     переменную окружения, а не аргумент и не URL, так он не попадает ни
//     в журнал, ни в список процессов; хранится только в памяти до конца
//     задания;
//   - SSH с deploy-ключом хоста: ключ генерируется один раз в каталоге
//     данных, публичную половину показывают, чтобы добавить в GitHub/
//     GitLab как deploy key; clone идёт с GIT_SSH_COMMAND и именно этим
//     ключом.

// KindClone — вид задания.
const KindClone = "files.clone"

// CloneAuth — как входить в репозиторий.
const (
	AuthNone  = "none"
	AuthToken = "token"
	AuthKey   = "deploy-key"
)

var (
	branchRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	sshURLRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._/-]+$`)
	dirNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// CloneParams — вход задания. Секрета здесь нет: он в памяти, по билету.
type CloneParams struct {
	URL      string `json:"url"`
	Branch   string `json:"branch,omitempty"`
	Dest     string `json:"dest"`
	Auth     string `json:"auth"`
	Username string `json:"username,omitempty"`
	Ticket   string `json:"ticket,omitempty"`
}

// Validate проверяет то, что уйдёт git.
func (p *CloneParams) Validate() error {
	p.URL = strings.TrimSpace(p.URL)
	switch {
	case strings.HasPrefix(p.URL, "https://"), strings.HasPrefix(p.URL, "http://"):
		u, err := url.Parse(p.URL)
		if err != nil || u.Host == "" {
			return msgs.Errorf("files.invalidRepositoryAddress")
		}
		if u.User != nil {
			// Секрет в адресе попал бы в журнал и в список процессов.
			return msgs.Errorf("files.doPutPasswordTokenInto")
		}
	case strings.HasPrefix(p.URL, "ssh://"), sshURLRe.MatchString(p.URL):
	default:
		return msgs.Errorf("files.addressMustHttpsGitHost")
	}
	if p.Branch != "" && !branchRe.MatchString(p.Branch) {
		return msgs.Errorf("files.invalidBranchName", p.Branch)
	}
	switch p.Auth {
	case "", AuthNone:
		p.Auth = AuthNone
	case AuthToken, AuthKey:
	default:
		return msgs.Errorf("files.unknownAccessMethod", p.Auth)
	}
	if strings.ContainsAny(p.Username, "/:@ \r\n") {
		return msgs.Errorf("files.invalidUserName")
	}
	return nil
}

// RepoDirName — имя каталога из адреса: последний сегмент без .git.
func RepoDirName(rawURL string) string {
	s := strings.TrimSuffix(strings.TrimRight(rawURL, "/"), ".git")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	if !dirNameRe.MatchString(s) {
		return ""
	}
	return s
}

// secrets — токены на время задания, по билету. В базу не попадают.
type secrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *secrets) put(ticket, secret string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[ticket] = secret
}

func (s *secrets) take(ticket string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[ticket]
	delete(s.m, ticket)
	return v, ok
}

// CloneRunner выполняет задания клонирования.
type CloneRunner struct {
	m       *Manager
	secrets secrets
}

// NewCloneRunner строит исполнителя.
func NewCloneRunner(m *Manager) *CloneRunner { return &CloneRunner{m: m} }

// Resumable — нет: наполовину склонированный каталог git сам не
// продолжит, а повторный clone в непустой каталог откажет.
func (r *CloneRunner) Resumable() bool { return false }

// Prepare проверяет вход и прячет секрет в памяти, возвращая билет.
func (r *CloneRunner) Prepare(p *CloneParams, secret string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if _, err := r.m.Check(p.Dest); err != nil {
		return err
	}
	if p.Auth == AuthToken {
		if strings.TrimSpace(secret) == "" {
			return msgs.Errorf("files.specifyTokenPassword")
		}
		p.Ticket = fmt.Sprintf("%d-%s", time.Now().UnixNano(), RepoDirName(p.URL))
		r.secrets.put(p.Ticket, secret)
	}
	return nil
}

// Run клонирует.
func (r *CloneRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p CloneParams
	if err := jc.Params(&p); err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	dest, err := r.m.Check(p.Dest)
	if err != nil {
		return err
	}
	if r.m.runEnv == nil {
		return msgs.Errorf("files.cloningUnavailableMode")
	}
	if r.m.c.Exists(dest) {
		if entries, err := r.m.c.ListDir(dest); err == nil && len(entries) > 0 {
			return msgs.Errorf("files.directoryAlreadyExistsEmpty", dest)
		}
	}

	jc.Step(1, 2, msgs.T(jc.Lang(), "files.stepPrepare"))
	env := map[string]string{"GIT_TERMINAL_PROMPT": "0"}
	cloneURL := p.URL
	var secret string
	switch p.Auth {
	case AuthToken:
		var ok bool
		secret, ok = r.secrets.take(p.Ticket)
		if !ok {
			return msgs.Errorf("files.tokenJobLongerAvailableStart")
		}
		helper, err := r.m.askpassHelper()
		if err != nil {
			return err
		}
		env["GIT_ASKPASS"] = helper
		env["NKT_GIT_SECRET"] = secret
		user := p.Username
		if user == "" {
			// GitHub и GitLab с токеном принимают любое имя; пустое —
			// git спросит его, а спрашивать некого.
			user = "x-access-token"
		}
		u, _ := url.Parse(cloneURL)
		u.User = url.User(user)
		cloneURL = u.String()
		jc.Log("files.signingTokenAsTokenItself", user)
	case AuthKey:
		keyPath, _, err := r.m.DeployKey()
		if err != nil {
			return err
		}
		env["GIT_SSH_COMMAND"] = fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new", keyPath)
		jc.Log("files.signingHostSDeployKey", keyPath)
	}

	argv := []string{"git", "clone", "--progress"}
	if p.Branch != "" {
		argv = append(argv, "--branch", p.Branch)
	}
	argv = append(argv, "--", cloneURL, dest)
	jc.Step(2, 2, "git clone")
	jc.Logf("git clone %s → %s", p.URL, dest)
	res, err := r.m.runEnv(ctx, env, argv...)
	if err != nil {
		return msgs.Errorf("files.runningGit", err)
	}
	for _, line := range tail(res.Output(), 12) {
		jc.Logf("      %s", scrub(line, cloneURL, p.URL, secret))
	}
	if res.ExitCode != 0 {
		return msgs.Errorf("files.gitCloneExitedCode", res.ExitCode, scrub(lastLine(res.Output()), cloneURL, p.URL, secret))
	}
	jc.Log("files.doneRepository", dest)
	return nil
}

// askpassHelper — скрипт, отдающий git секрет из окружения. Лежит в
// каталоге данных: он виден и снаружи песочницы, где git и работает.
func (m *Manager) askpassHelper() (string, error) {
	if err := os.MkdirAll(m.tmpDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(m.tmpDir, "git-askpass.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$NKT_GIT_SECRET\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

// DeployKey отдаёт путь к приватному deploy-ключу хоста и публичную
// половину, заводя пару при первом обращении.
func (m *Manager) DeployKey() (privPath, public string, err error) {
	dir := filepath.Join(m.tmpDir, "deploy-key")
	privPath = filepath.Join(dir, "id_ed25519")
	pubPath := privPath + ".pub"
	if raw, err := os.ReadFile(pubPath); err == nil {
		return privPath, strings.TrimSpace(string(raw)), nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	host, _ := os.Hostname()
	priv, pub, err := vmcreate.GenerateKeyPair("deploy-" + host)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(privPath, []byte(priv), 0o600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(pubPath, []byte(pub+"\n"), 0o644); err != nil {
		return "", "", err
	}
	return privPath, strings.TrimSpace(pub), nil
}

func tail(out string, n int) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(strings.ReplaceAll(line, "\r", "\n")); line != "" {
			for _, sub := range strings.Split(line, "\n") {
				if sub = strings.TrimSpace(sub); sub != "" {
					lines = append(lines, sub)
				}
			}
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// scrub прячет адрес с именем пользователя за исходным адресом (имя
// незачем светить в журнале) и сам секрет, если git вдруг повторил его
// в сообщении об ошибке.
func scrub(line, withUser, plain, secret string) string {
	line = strings.ReplaceAll(line, withUser, plain)
	if secret != "" {
		line = strings.ReplaceAll(line, secret, "***")
	}
	return line
}

// DestFor — куда клонировать: каталог + имя из адреса.
func DestFor(dir, rawURL, name string) (string, error) {
	if name == "" {
		name = RepoDirName(rawURL)
	}
	if !dirNameRe.MatchString(name) {
		return "", msgs.Errorf("files.specifyDirectoryNameLatinLetters")
	}
	return gopath.Join(dir, name), nil
}
