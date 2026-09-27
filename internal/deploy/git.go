package deploy

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Git на хабе — программой git (в образе хаба она есть). Учётные данные
// не попадают в командную строку: токен — заголовком через
// GIT_CONFIG_COUNT/KEY/VALUE (окружение, не argv), ключ SSH — временным
// файлом 0600 через GIT_SSH_COMMAND. Все значения из описания проверены
// (ValidRepo, ValidRef), а перед адресом репозитория стоит «--».

// Cred — доступ к репозиторию: токен (https) или ключ развёртывания (ssh).
type Cred struct {
	Token  string `json:"token,omitempty"`
	SSHKey string `json:"ssh_key,omitempty"`
}

// Git — вызовы git в рабочем каталоге конвейера.
type Git struct {
	// Dir — каталог конвейера на хабе (ключи, known_hosts, checkout).
	Dir  string
	Cred Cred
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ValidSHA — полный хэш коммита.
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

func (g Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", msgs.Errorf("deploy.noGit")
	}
	if err := os.MkdirAll(g.Dir, 0o700); err != nil {
		return "", err
	}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "LC_ALL=C")
	if g.Cred.Token != "" {
		auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + g.Cred.Token))
		env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic "+auth)
	}
	sshOpts := "ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=" + filepath.Join(g.Dir, "known_hosts")
	if g.Cred.SSHKey != "" {
		key := filepath.Join(g.Dir, "deploy_key")
		if err := os.WriteFile(key, []byte(strings.TrimSpace(g.Cred.SSHKey)+"\n"), 0o600); err != nil {
			return "", err
		}
		defer os.Remove(key)
		sshOpts += " -o IdentitiesOnly=yes -i " + key
	}
	env = append(env, "GIT_SSH_COMMAND="+sshOpts)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", msgs.Errorf("deploy.git", args[0], strings.TrimSpace(redact(errb.String(), g.Cred.Token)))
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
	out, err := g.run(ctx, g.Dir, "ls-remote", "--heads", "--tags", "--", repo)
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
	_ = os.RemoveAll(dest)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	if _, err := g.run(ctx, dest, "init", "-q"); err != nil {
		return err
	}
	if _, err := g.run(ctx, dest, "fetch", "-q", "--depth", "1", "--", repo, sha); err != nil {
		if ref == "" {
			return err
		}
		if _, err2 := g.run(ctx, dest, "fetch", "-q", "--depth", "200", "--", repo, ref); err2 != nil {
			return err2
		}
	}
	_, err := g.run(ctx, dest, "-c", "advice.detachedHead=false", "checkout", "-q", sha)
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
