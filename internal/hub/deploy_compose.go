package hub

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Выкладка compose-стека (action: compose): файлы стека из репозитория —
// на каждый хост по очереди (POST /api/compose/stacks/deploy), хаб ждёт
// задание хоста (pull, up --wait) и только потом идёт к следующему;
// первая неудача останавливает выкладку.

// composeMaxBytes — предел файлов стека (как у хоста).
const composeMaxBytes = 4 << 20

// collectComposeFiles — файлы стека: путь внутри стека → содержимое.
// Стек — каталог compose-файла: всё, что перечислено в files, должно
// лежать внутри него (относительные ссылки compose иначе сломаются).
func collectComposeFiles(src string, c *deploy.ComposeSpec, vars deploy.Vars) (map[string]string, string, error) {
	base := path.Dir(c.File)
	rel := func(p string) (string, error) {
		if base == "." {
			return p, nil
		}
		if !strings.HasPrefix(p, base+"/") {
			return "", msgs.Errorf("deploy.composeOutside", p, base)
		}
		return strings.TrimPrefix(p, base+"/"), nil
	}
	files := map[string]string{}
	total := 0
	add := func(repoPath string) error {
		text, err := deploy.ReadFile(src, repoPath)
		if err != nil {
			return err
		}
		r, err := rel(repoPath)
		if err != nil {
			return err
		}
		if !utf8.ValidString(text) {
			// Файлы стека едут на хост текстом; двоичным (картинки,
			// архивы) место в образе, а не рядом с compose.
			return msgs.Errorf("deploy.composeBinary", repoPath)
		}
		text = vars.Substitute(text)
		total += len(text)
		if total > composeMaxBytes {
			return msgs.Errorf("compose.tooLarge")
		}
		files[r] = text
		return nil
	}
	if err := add(c.File); err != nil {
		return nil, "", err
	}
	main, _ := rel(c.File)
	if len(c.Images) > 0 || len(c.Ports) > 0 || len(c.EnvKeys) > 0 {
		images := map[string]string{}
		for svc, img := range c.Images {
			images[svc] = vars.Substitute(img)
		}
		out, err := deploy.OverrideServices(files[main], images, c.Ports, c.EnvKeys)
		if err != nil {
			return nil, "", err
		}
		files[main] = out
	}
	if names := deploy.BuildOnlyServices(files[main]); len(names) > 0 {
		// Ловим до хостов: там up попытался бы собрать образ без
		// Dockerfile и упал бы с невнятной ошибкой.
		return nil, "", msgs.Errorf("compose.buildOnly", strings.Join(names, ", "))
	}
	for _, f := range c.Files {
		f = strings.TrimSuffix(f, "/")
		full := filepath.Join(src, filepath.FromSlash(f))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, "", msgs.Errorf("deploy.fileMissing", f)
		}
		if !info.IsDir() {
			if err := add(f); err != nil {
				return nil, "", err
			}
			continue
		}
		err = filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
				return err
			}
			r, err := filepath.Rel(src, p)
			if err != nil {
				return err
			}
			if len(files) >= 200 {
				return msgs.Errorf("compose.tooLarge")
			}
			return add(filepath.ToSlash(r))
		})
		if err != nil {
			return nil, "", err
		}
	}
	return files, main, nil
}

// deployCompose выкладывает стек на хосты конвейера по очереди.
func (r *DeployRunner) deployCompose(ctx context.Context, jc *jobs.Context, pl store.Pipeline, spec deploy.Spec, src string, vars deploy.Vars) error {
	s := r.s
	c := spec.Compose
	files, main, err := collectComposeFiles(src, c, vars)
	if err != nil {
		return err
	}
	env, err := s.pipelineEnv(pl)
	if err != nil {
		return err
	}
	// Секреты из env_keys — только из .env конвейера: без них стек
	// поднялся бы с пустыми значениями.
	if missing := missingEnvKeys(c.AllEnvKeys(), env); len(missing) > 0 {
		return msgs.Errorf("deploy.envKeysMissing", strings.Join(missing, ", "))
	}
	targets, err := s.resolveHosts(ctx, c.Hosts, c.Group)
	if err != nil {
		return err
	}
	// От чьего имени ходить во встроенный API машины хаба: у выкладки по
	// вебхуку, опросу или registry автор — «github», «poll» — не учётная
	// запись; тогда — автор конвейера.
	user := s.actingUser(ctx, jc.Job.Author, pl.Author, s.firstAdmin(ctx))
	lang := jc.Lang()
	// Движок — до первого хоста: без docker или compose где-то в конце
	// списка выкладка иначе оставила бы хосты в разных версиях.
	if bad := s.composeEngineProblems(ctx, jc, user, targets); len(bad) > 0 {
		return msgs.Errorf("deploy.composeEngines", strings.Join(bad, "; "))
	}
	jc.Log("deploy.composeFiles", len(files), c.Project, len(targets))
	if err := bindComposePorts(jc, files, main, c); err != nil {
		return err
	}
	// Где стек был до этой выкладки: переименованный — заменяется на том
	// же хосте, с прежних хостов — убирается после успеха.
	prev := s.loadDeployed(ctx, pl.ID)
	for _, h := range stackMovesFrom(prev, targets) {
		jc.Log("deploy.stackMoves", prev.Project, h.Name)
	}
	for i, t := range targets {
		jc.StepKey(2+i, 2+len(targets), "deploy.stepCompose", t.Name)
		body := composeBody(c, main, files, env, pl.EnvSHA, msgs.T(lang, "deploy.composeNote", pl.Name, deploy.ShortSHA(vars.Commit)))
		if old := replaceOn(prev, t.ID, c.Project); old != "" {
			body["replace"] = old
			jc.Log("deploy.stackRenamed", old, c.Project, t.Name)
		}
		var started struct {
			JobID     int64  `json:"job_id"`
			Engine    string `json:"engine"`
			EnvEdited bool   `json:"env_edited"`
		}
		if _, err := s.composeHostPost(ctx, jc, user, t, "/api/compose/stacks/deploy", body, &started); err != nil {
			return msgs.Errorf("deploy.composeHostFailed", t.Name, msgs.Localize(lang, err))
		}
		if started.EnvEdited {
			jc.Log("deploy.envEditedOverwritten", t.Name)
		}
		if env != nil {
			// .env на хосте теперь этот: с ним сравнит следующая выкладка.
			_ = s.db.SetPipelineEnvSHA(ctx, pl.ID, envSHA(*env))
		}
		if err := s.waitHostJobVia(ctx, jc, user, t.ID, started.JobID); err != nil {
			// Следующие хосты не трогаем: сломанное не должно расползтись.
			return msgs.Errorf("deploy.composeHostFailed", t.Name, msgs.Localize(lang, err))
		}
		jc.Log("deploy.composeHostDone", t.Name)
	}
	// Все хосты подняты — запомнить, где стек теперь, и убрать старый с
	// прежних хостов (недоступный — «остался», выкладка от этого не падает).
	cur := deployedStack{Project: c.Project}
	for _, t := range targets {
		cur.Hosts = append(cur.Hosts, deployedHost{ID: t.ID, Name: t.Name})
	}
	s.saveDeployed(ctx, pl.ID, cur)
	s.cleanupMovedStacks(ctx, jc, user, pl, c.Project, prev, targets)
	switch {
	case c.Site.Managed():
		// Сайт — после стека, на единственном хосте; не настроился — в
		// журнале и у сайта, а выкладка удалась: стек уже обновлён.
		if err := s.pipelineSite(ctx, jc, user, pl, c, targets[0]); err != nil {
			jc.Log("deploy.siteSetupFailed", c.Site.Domains[0], msgs.Localize(lang, err))
		}
	case c.Site.CheckDomain() != "":
		d := c.Site.CheckDomain()
		chk := httpsCheck(ctx, d)
		if chk.OK {
			jc.Log("deploy.siteOK", d, chk.Status, chk.CertDaysLeft)
		} else {
			jc.Log("deploy.siteFailed", d, chk.Error)
		}
	}
	return nil
}

// bindComposePorts — адрес публикаций портов в копии compose-файла и
// строка журнала про каждый порт.
func bindComposePorts(jc *jobs.Context, files map[string]string, main string, c *deploy.ComposeSpec) error {
	if other := c.SitePortsMismatch(); len(other) > 0 {
		list := make([]string, len(other))
		for i, p := range other {
			list[i] = strconv.Itoa(p)
		}
		jc.Log("deploy.sitePortsMismatch", c.Site.Service, strings.Join(list, ", "), c.Site.Port)
	}
	out, changes, err := deploy.BindPorts(files[main], c.BindAddr(), c.BindForce)
	if err != nil {
		return err
	}
	files[main] = out
	for _, ch := range changes {
		switch {
		case ch.Skipped:
			jc.Log("deploy.portSkipped", ch.From, ch.Service)
		case ch.Kept:
			jc.Log("deploy.portKept", ch.From, ch.Service)
		default:
			jc.Log("deploy.portBound", ch.From, ch.Service, ch.To)
		}
	}
	return nil
}

// composeOptionalFields — поля запроса к хосту, появившиеся позже самого
// запроса: старый хост их не знает и отвергает тело целиком («unknown
// field»). Тогда запрос повторяется без них — выкладка работает, а
// проверки, которых старому хосту не сделать, в журнале названы.
var composeOptionalFields = []string{"env_sha", "site_service", "site_port", "skip"}

// composeHostPost — POST к хосту с откатом на старый хост.
func (s *Server) composeHostPost(ctx context.Context, jc *jobs.Context, user string, t targetHost, path string, body map[string]any, out any) (int, error) {
	code, err := s.hostCall(ctx, user, t.ID, "POST", path, body, out)
	if err == nil || code != http.StatusBadRequest || !strings.Contains(msgs.Localize(msgs.EN, err), "unknown field") {
		return code, err
	}
	var dropped []string
	for _, k := range composeOptionalFields {
		if _, ok := body[k]; ok {
			delete(body, k)
			dropped = append(dropped, k)
		}
	}
	if len(dropped) == 0 {
		return code, err
	}
	jc.Log("deploy.hostOld", t.Name, strings.Join(dropped, ", "))
	return s.hostCall(ctx, user, t.ID, "POST", path, body, out)
}

// pipelineEnv — расшифрованный .env стека конвейера (nil — не задан).
func (s *Server) pipelineEnv(pl store.Pipeline) (*string, error) {
	if len(pl.EnvEnc) == 0 {
		return nil, nil
	}
	raw, err := secretbox.Decrypt(s.hub.key, pl.EnvEnc)
	if err != nil {
		return nil, err
	}
	e := string(raw)
	return &e, nil
}

// composeBody — запрос выкладки (и сухого прогона) стека к хосту.
func composeBody(c *deploy.ComposeSpec, main string, files map[string]string, env *string, envSHA, note string) map[string]any {
	body := map[string]any{
		"project": c.Project, "file": main, "files": files, "pull": c.PullImages(),
		"wait_timeout": int(c.Wait().Seconds()), "note": note,
	}
	if env != nil {
		body["env"] = *env
		// По нему хост узнаёт, что .env правили вручную.
		body["env_sha"] = envSHA
	}
	return body
}

// composeEngine — ответ хоста GET /api/compose/engine.
type composeEngine struct {
	Engine         string `json:"engine"`
	Version        string `json:"version"`
	Compose        bool   `json:"compose"`
	ComposeVersion string `json:"compose_version"`
	ComposeError   string `json:"compose_error"`
	Installable    bool   `json:"installable"`
	DaemonDown     bool   `json:"daemon_down"`
	DaemonError    string `json:"daemon_error"`
}

// composeEngineProblems — хосты, где стек не поднять: нет docker/podman
// или не работает compose. Хост старой версии (нет маршрута) не
// проверяется — выкладка на нём скажет сама.
func (s *Server) composeEngineProblems(ctx context.Context, jc *jobs.Context, user string, targets []targetHost) []string {
	lang := jc.Lang()
	var bad []string
	for _, t := range targets {
		var e composeEngine
		code, err := s.hostCall(ctx, user, t.ID, "GET", "/api/compose/engine", nil, &e)
		switch {
		case code == http.StatusNotFound || code == http.StatusMethodNotAllowed:
			continue
		case err != nil:
			bad = append(bad, t.Name+": "+msgs.Localize(lang, err))
		case e.Engine == "":
			bad = append(bad, msgs.T(lang, "deploy.hostNoEngine", t.Name))
		case !e.Compose:
			bad = append(bad, msgs.T(lang, "deploy.hostNoCompose", t.Name, e.Version, e.ComposeError))
		case e.DaemonDown:
			bad = append(bad, msgs.T(lang, "deploy.hostDaemonDown", t.Name, e.Engine, e.DaemonError))
		}
	}
	return bad
}

// HTTPSCheck — ответ сайта снаружи (с хаба).
type HTTPSCheck struct {
	OK           bool   `json:"ok"`
	Status       int    `json:"status,omitempty"`
	Error        string `json:"error,omitempty"`
	CertNotAfter string `json:"cert_not_after,omitempty"`
	CertDaysLeft int    `json:"cert_days_left,omitempty"`
	CertIssuer   string `json:"cert_issuer,omitempty"`
	// CertNames — на какие имена выдан отданный сертификат; WrongCert —
	// он не для этого имени (прокси отвечает чужим сайтом).
	CertNames []string `json:"cert_names,omitempty"`
	WrongCert bool     `json:"wrong_cert,omitempty"`
	CheckedAt string   `json:"checked_at"`
}

// Для тестов: куда подключаться и чему доверять (nil — как обычно).
var (
	httpsCheckDial  func(ctx context.Context, network, addr string) (net.Conn, error)
	httpsCheckRoots *x509.CertPool
)

// httpsCheck — GET https://<домен>/ с проверкой сертификата: ответил ли
// (любой код, кроме 5xx) и сколько сертификату осталось.
func httpsCheck(ctx context.Context, domain string) HTTPSCheck {
	out := HTTPSCheck{CheckedAt: store.Now()}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, "https://"+domain+"/", nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: httpsCheckRoots}}
	if httpsCheckDial != nil {
		tr.DialContext = httpsCheckDial
	}
	client := &http.Client{
		Timeout:   20 * time.Second,
		Transport: tr,
		// Редирект (например, на /login) — тоже ответ сайта.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		out.Error = err.Error()
		var he x509.HostnameError
		if errors.As(err, &he) && he.Certificate != nil {
			// Сертификат не для этого имени: чей он — видно из самого
			// сертификата (подключение уже состоялось, проверка — нет).
			out.WrongCert = true
			out.CertNames = certNames(he.Certificate)
			out.CertNotAfter = he.Certificate.NotAfter.UTC().Format(time.RFC3339)
		}
		return out
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	out.Status = resp.StatusCode
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		out.CertNotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
		out.CertDaysLeft = int(time.Until(cert.NotAfter).Hours() / 24)
		out.CertIssuer = cert.Issuer.CommonName
		out.CertNames = certNames(cert)
	}
	out.OK = resp.StatusCode < 500
	if !out.OK {
		out.Error = resp.Status
	}
	return out
}

// certNames — имена сертификата (SAN, иначе CN).
func certNames(c *x509.Certificate) []string {
	if len(c.DNSNames) > 0 {
		return c.DNSNames
	}
	if c.Subject.CommonName != "" {
		return []string{c.Subject.CommonName}
	}
	return nil
}

// firstAdmin — любой действующий администратор хаба (пусто — нет).
func (s *Server) firstAdmin(ctx context.Context) string {
	users, err := s.db.ListUsers(ctx)
	if err != nil {
		return ""
	}
	for _, u := range users {
		if u.IsAdmin() && !u.Disabled {
			return u.Username
		}
	}
	return ""
}

// actingUser — первая из кандидатур, что является действующей учётной
// записью администратора (пусто — ни одной).
func (s *Server) actingUser(ctx context.Context, candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if u, err := s.db.UserByName(ctx, c); err == nil && u.IsAdmin() && !u.Disabled {
			return u.Username
		}
	}
	return ""
}
