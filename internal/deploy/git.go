package deploy

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Git на хабе — программой git (в образе хаба она есть). Учётные данные
// не попадают в командную строку: токен — через askpass из окружения
// процесса git, ключ SSH — временным файлом 0600 через GIT_SSH_COMMAND.
// Все значения из описания проверены (ValidRepo, ValidRef, ValidSHA);
// перед адресом и ссылкой стоит «--» прямо в вызове git, а коммит для
// checkout передаётся не аргументом, а записью в HEAD.

// Cred — доступ к репозиторию: токен (https) или ключ развёртывания (ssh).
type Cred struct {
	Token  string `json:"token,omitempty"`
	SSHKey string `json:"ssh_key,omitempty"`
}

// RepoCred — доступ к другим репозиториям (submodules, соседние
// репозитории организации): по началу адреса «хост/путь»
// («github.com/vendor/» — все репозитории этой организации).
type RepoCred struct {
	Prefix string `json:"prefix"`
	Cred
}

// Git — вызовы git в рабочем каталоге конвейера.
type Git struct {
	// Dir — каталог конвейера на хабе (ключи, known_hosts, checkout).
	Dir  string
	Cred Cred
	// Extra — ключи других репозиториев (submodules): у каждого адреса —
	// самый длинный подходящий префикс; основной репозиторий — Cred.
	Extra []RepoCred
	// Repo — адрес репозитория (для вида токена; ставится Remote и
	// Checkout сами).
	Repo string
}

// PlaceholderRepo — адрес-заглушка из шаблона описания.
const PlaceholderRepo = "https://github.com/org/app.git"

// TokenLogin — логин и пароль для токена. «логин:токен» в поле — как
// есть (сервер, которому важен логин); иначе логин по серверу: GitLab ждёт
// «oauth2», GitHub, Forgejo, Gitea и прочие берут токен из пароля.
func TokenLogin(repo, token string) (string, string) {
	if user, pass, ok := strings.Cut(token, ":"); ok && user != "" && pass != "" && !strings.ContainsAny(user, " \t") {
		return user, pass
	}
	return tokenUser(repo), token
}

// gitPromptScript — отвечает git на «Username for 'адрес'» и «Password for
// 'адрес'» значениями из окружения: ключи NKT_GIT_P<i> (префикс «хост/путь»,
// от длинного к короткому), NKT_GIT_U<i>, NKT_GIT_W<i>; первый подходящий
// префикс — его логин и токен. git с credential.useHttpPath называет в
// вопросе адрес целиком. В самом файле секретов нет.
const gitPromptScript = `#!/bin/sh
url=${1#*\'}
url=${url%\'*}
url=${url#*://}
url=${url#*@}
url=${url%.git}
host=${url%%/*}
rest=${url#*/}
url=${host%%:*}/$rest
i=0
while [ "$i" -lt "${NKT_GIT_N:-0}" ]; do
  eval "p=\${NKT_GIT_P$i}"
  case "$url/" in
    "$p"*)
      case "$1" in
        Username*|username*) eval "printf '%s\n' \"\${NKT_GIT_U$i}\"" ;;
        *) eval "printf '%s\n' \"\${NKT_GIT_W$i}\"" ;;
      esac
      exit 0 ;;
  esac
  i=$((i+1))
done
printf '\n'
`

// gitSSHScript — ssh с ключом под адрес: git вызывает его как
// «… [-p порт] пользователь@хост "git-upload-pack 'путь'"»; ключ — файл
// NKT_SSH_K<i> первого подходящего префикса NKT_SSH_P<i>, иначе без ключа.
// Ключ развёртывания GitHub привязан к одному репозиторию: перебирать все
// ключи подряд нельзя — первый чужой «войдёт» и ответит «нет репозитория».
const gitSSHScript = `#!/bin/sh
last=""
host=""
for a in "$@"; do host=$last; last=$a; done
path=${last#*\'}
path=${path%\'*}
path=${path#/}
path=${path%.git}
h=${host#*@}
key=""
i=0
while [ "$i" -lt "${NKT_SSH_N:-0}" ]; do
  eval "p=\${NKT_SSH_P$i}"
  case "$h/$path/" in
    "$p"*) eval "key=\${NKT_SSH_K$i}"; break ;;
  esac
  i=$((i+1))
done
if [ -n "$key" ]; then
  exec ssh $NKT_SSH_OPTS -o IdentitiesOnly=yes -i "$key" "$@"
fi
exec ssh $NKT_SSH_OPTS "$@"
`

// RepoKey — адрес репозитория как «хост/путь» без схемы, логина и .git:
// по нему подбираются ключи (git@host:org/app.git, ssh://git@host/org/app,
// https://host/org/app.git → host/org/app).
func RepoKey(repo string) string {
	r := strings.TrimSpace(repo)
	if scpRepoRe.MatchString(r) {
		at := strings.Index(r, "@")
		host, path, _ := strings.Cut(r[at+1:], ":")
		return strings.ToLower(host) + "/" + strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	}
	if u, err := url.Parse(r); err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname()) + "/" + strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	}
	// «хост[:порт]/путь» без схемы: порт не участвует (у ssh его нет в
	// адресе вовсе — он идёт отдельным -p).
	r = strings.TrimSuffix(strings.Trim(r, "/"), ".git")
	host, path, _ := strings.Cut(r, "/")
	host, _, _ = strings.Cut(host, ":")
	if path == "" {
		return strings.ToLower(host)
	}
	return strings.ToLower(host) + "/" + path
}

// NormalizePrefix — префикс ключа из поля: адрес или «хост/путь», без
// схемы и .git, с «/» в конце (чтобы org/app не подходил к org/app2).
func NormalizePrefix(p string) string {
	k := RepoKey(p)
	if k == "" {
		return ""
	}
	return strings.TrimSuffix(k, "/") + "/"
}

// creds — все ключи: основной (по адресу основного репозитория) и
// дополнительные, от длинного префикса к короткому.
func (g Git) creds() []RepoCred {
	out := append([]RepoCred(nil), g.Extra...)
	if g.Repo != "" && (g.Cred.Token != "" || g.Cred.SSHKey != "") {
		out = append(out, RepoCred{Prefix: RepoKey(g.Repo), Cred: g.Cred})
	}
	for i := range out {
		out[i].Prefix = NormalizePrefix(out[i].Prefix)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].Prefix) > len(out[j].Prefix) })
	return out
}

func writeScript(dir, name, body string) (string, error) {
	p := filepath.Join(dir, name)
	if b, err := os.ReadFile(p); err == nil && string(b) == body {
		return p, nil
	}
	if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
		return "", err
	}
	return p, nil
}

// TokenHint — последние четыре знака токена: сверить с сервером, тот ли
// токен сохранён (сам токен не показывается).
func TokenHint(token string) string {
	_, pass := TokenLogin("", token)
	if len(pass) < 8 {
		return ""
	}
	return pass[len(pass)-4:]
}

// tokenUser — логин для токена: GitLab ждёт «oauth2»; GitHub, Forgejo,
// Gitea и прочие берут токен из пароля, логин не важен.
func tokenUser(repo string) string {
	if u, err := url.Parse(repo); err == nil && strings.Contains(strings.ToLower(u.Hostname()), "gitlab") {
		return "oauth2"
	}
	return "x-access-token"
}

// Причины отказа git, понятные человеку.
const (
	GitNoAccess      = "no_access"      // ключей нет: закрыт или его нет
	GitTokenRejected = "token_rejected" // токен есть, но не подошёл
	GitKeyRejected   = "key_rejected"   // ключ ssh не подошёл
	GitNotFound      = "not_found"      // сервер ответил, что репозитория нет
	GitHostUnknown   = "host_unknown"   // имя сервера не разрешается или нет соединения
	GitOther         = "other"
)

var (
	gitAuthRe    = regexp.MustCompile(`(?i)could not read username|terminal prompts disabled|authentication failed|invalid username or (token|password)|http basic: access denied|\b(401|403)\b`)
	gitKeyRe     = regexp.MustCompile(`(?i)permission denied \(publickey|host key verification failed|no supported authentication`)
	gitNotFound  = regexp.MustCompile(`(?i)repository not found|does not appear to be a git repository|\b404\b|not found`)
	gitNetworkRe = regexp.MustCompile(`(?i)could not resolve host|name or service not known|connection refused|connection timed out|network is unreachable|no route to host|ssl certificate problem|server certificate verification failed`)
)

// ClassifyGitError — причина отказа git по его выводу и тому, были ли
// ключи. Сервер на закрытый и несуществующий репозиторий без входа
// отвечает одинаково (просьбой войти), поэтому без ключей это одна причина.
func ClassifyGitError(text string, hasToken, hasKey bool) string {
	switch {
	case gitNetworkRe.MatchString(text):
		return GitHostUnknown
	case gitKeyRe.MatchString(text):
		if hasKey {
			return GitKeyRejected
		}
		return GitNoAccess
	case gitAuthRe.MatchString(text):
		if hasToken {
			return GitTokenRejected
		}
		return GitNoAccess
	case gitNotFound.MatchString(text):
		if hasToken || hasKey {
			return GitNotFound
		}
		return GitNoAccess
	}
	return GitOther
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ValidSHA — полный хэш коммита.
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// gitTimeout — предел одного вызова git.
const gitTimeout = 5 * time.Minute

// run выполняет подготовленный вызовом exec.CommandContext(…, "git", …)
// git: окружение (доступ, ssh) и каталог ставит сам. Команду собирает
// вызывающий — с «--» перед значениями из описания прямо в вызове.
func (g Git) run(cmd *exec.Cmd, dir, what string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", msgs.Errorf("deploy.noGit")
	}
	if err := os.MkdirAll(g.Dir, 0o700); err != nil {
		return "", err
	}
	// Протоколы — только сетевые: адреса submodules берутся из самого
	// репозитория, file:// и ext:: открыли бы файлы и команды хаба.
	protocols := "https:http:ssh"
	if allowLocalRepos {
		protocols += ":file" // тесты: репозитории в каталогах
	}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_ALLOW_PROTOCOL="+protocols)
	// Вход — как у обычного git: сервер отвечает 401, git спрашивает логин
	// и пароль у askpass (по адресу — свой ключ: основной репозиторий,
	// submodules). Заранее отправленный заголовок отбрасывают некоторые
	// прокси перед сервером и переадресации, а ответ на запрос входа
	// доходит всегда. Сам askpass — сценарий без секретов: значения берёт
	// из окружения процесса git.
	askpass, err := writeScript(g.Dir, "askpass.sh", gitPromptScript)
	if err != nil {
		return "", err
	}
	sshWrap, err := writeScript(g.Dir, "ssh.sh", gitSSHScript)
	if err != nil {
		return "", err
	}
	env = append(env, "GIT_ASKPASS="+askpass)
	var secrets []string
	n, ns := 0, 0
	for _, c := range g.creds() {
		if c.Token != "" {
			user, pass := TokenLogin("https://"+c.Prefix, c.Token)
			env = append(env, fmt.Sprintf("NKT_GIT_P%d=%s", n, c.Prefix), fmt.Sprintf("NKT_GIT_U%d=%s", n, user), fmt.Sprintf("NKT_GIT_W%d=%s", n, pass))
			secrets = append(secrets, pass)
			n++
		}
		if c.SSHKey != "" {
			key := filepath.Join(g.Dir, fmt.Sprintf("deploy_key_%d", ns))
			if err := os.WriteFile(key, []byte(strings.TrimSpace(c.SSHKey)+"\n"), 0o600); err != nil {
				return "", err
			}
			defer os.Remove(key)
			env = append(env, fmt.Sprintf("NKT_SSH_P%d=%s", ns, c.Prefix), fmt.Sprintf("NKT_SSH_K%d=%s", ns, key))
			ns++
		}
	}
	env = append(env, fmt.Sprintf("NKT_GIT_N=%d", n), fmt.Sprintf("NKT_SSH_N=%d", ns))
	// Только доступ конвейера: хранилища паролей системы и пользователя
	// (credential.helper store и т. п.) не подмешиваются — иначе закрытый
	// репозиторий «открывался» бы чужими сохранёнными паролями. useHttpPath
	// — чтобы askpass видел адрес с путём (ключи по репозиториям).
	env = append(env, "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=credential.useHttpPath", "GIT_CONFIG_VALUE_1=true")
	env = append(env, "NKT_SSH_OPTS=-o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="+filepath.Join(g.Dir, "known_hosts"),
		"GIT_SSH_COMMAND="+sshWrap)
	cmd.Dir = dir
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		text := errb.String()
		for _, sec := range append(secrets, g.Cred.Token) {
			text = redact(text, sec)
		}
		return "", msgs.Errorf("deploy.git", what, strings.TrimSpace(text))
	}
	return out.String(), nil
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}

// Remote — вершины веток и тегов репозитория: полное имя ссылки → коммит
// (у аннотированных тегов — коммит, на который они указывают).
func (g Git) Remote(ctx context.Context, repo string) (map[string]string, error) {
	if err := ValidRepo(repo); err != nil {
		return nil, err
	}
	g.Repo = repo
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := g.run(exec.CommandContext(ctx, "git", "ls-remote", "--heads", "--tags", "--", repo), g.Dir, "ls-remote")
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || !ValidSHA(sha) {
			continue
		}
		if base, peeled := strings.CutSuffix(ref, "^{}"); peeled {
			refs[base] = sha
			continue
		}
		if _, have := refs[ref]; !have {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// Checkout кладёт коммит sha репозитория в dest (каталог пересоздаётся).
// Сначала — выборка ровно этого коммита (GitHub, GitLab, Gitea так
// умеют); не вышло — ветка или тег ref целиком, затем нужный коммит.
func (g Git) Checkout(ctx context.Context, repo, ref, sha, dest string) error {
	if err := ValidRepo(repo); err != nil {
		return err
	}
	if !ValidSHA(sha) || (ref != "" && !ValidRef(ref)) {
		return msgs.Errorf("deploy.badCommit", sha)
	}
	g.Repo = repo
	_ = os.RemoveAll(dest)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*gitTimeout)
	defer cancel()
	if _, err := g.run(exec.CommandContext(ctx, "git", "init", "-q"), dest, "init"); err != nil {
		return err
	}
	// origin — адрес основного репозитория: относительные адреса
	// submodules («../lib.git») git разрешает от него.
	if _, err := g.run(exec.CommandContext(ctx, "git", "remote", "add", "origin", "--", repo), dest, "remote"); err != nil {
		return err
	}
	if _, err := g.run(exec.CommandContext(ctx, "git", "fetch", "-q", "--depth", "1", "--", repo, sha), dest, "fetch"); err != nil {
		if ref == "" {
			return err
		}
		if _, err2 := g.run(exec.CommandContext(ctx, "git", "fetch", "-q", "--depth", "200", "--", repo, ref), dest, "fetch"); err2 != nil {
			return err2
		}
	}
	// Отделённый HEAD — это файл HEAD с хэшем коммита (так его пишет и
	// сам git); reset --hard разворачивает по нему рабочее дерево. Хэш
	// не уходит в командную строку git вовсе.
	if err := os.WriteFile(filepath.Join(dest, ".git", "HEAD"), []byte(sha+"\n"), 0o644); err != nil {
		return err
	}
	if _, err := g.run(exec.CommandContext(ctx, "git", "reset", "-q", "--hard"), dest, "checkout"); err != nil {
		return err
	}
	return g.submodules(ctx, dest)
}

// submodules подтягивает submodules, если они есть (.gitmodules): у
// каждого — свой ключ из Extra по адресу (см. creds). Сначала только
// нужные коммиты (--depth 1), не вышло (сервер не отдаёт коммит по хэшу) —
// целиком.
func (g Git) submodules(ctx context.Context, dest string) error {
	if _, err := os.Stat(filepath.Join(dest, ".gitmodules")); err != nil {
		return nil
	}
	if _, err := g.run(exec.CommandContext(ctx, "git", "submodule", "update", "--init", "--recursive", "--depth", "1"), dest, "submodule"); err == nil {
		return nil
	}
	_, err := g.run(exec.CommandContext(ctx, "git", "submodule", "update", "--init", "--recursive"), dest, "submodule")
	return err
}

// ReadFile читает файл из checkout: путь проверен, выход за каталог
// (в том числе ссылкой) запрещён.
func ReadFile(root, rel string) (string, error) {
	if !ValidPath(rel) {
		return "", msgs.Errorf("deploy.specBad", "path", rel)
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return "", msgs.Errorf("deploy.fileMissing", rel)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(full, rootReal+string(os.PathSeparator)) {
		return "", msgs.Errorf("deploy.specBad", "path", rel)
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() || info.Size() > 4<<20 {
		return "", msgs.Errorf("deploy.fileMissing", rel)
	}
	b, err := os.ReadFile(full)
	return string(b), err
}

// Vars — подстановки выкладки: {{nkt.tag}}, {{nkt.commit}}, {{nkt.ref}}.
type Vars struct {
	Tag, Commit, Ref string
}

// Substitute подставляет значения в текст манифеста или values.
func (v Vars) Substitute(text string) string {
	return strings.NewReplacer("{{nkt.tag}}", v.Tag, "{{nkt.commit}}", v.Commit, "{{nkt.ref}}", v.Ref).Replace(text)
}

// ShortSHA — первые 12 знаков коммита.
func ShortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
