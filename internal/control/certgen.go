package control

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	gopath "path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/inventory"
	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/parse"
	"github.com/althq/netknownsthat/internal/store"
)

// CertManager issues certificate material directly on the host.
//
// GenerateSelfSigned never edits a live nginx or haproxy configuration:
// locating the right server block and rewriting it safely is a much larger
// feature than issuing a certificate, and every config edit already goes
// through ConfigManager's validated, auto-rolled-back path — it writes the
// new files and hands back the exact directives to paste there. RenewCertbot
// is different: it re-issues a lineage certbot already manages at the exact
// path nginx/haproxy already reference, so there is nothing to paste.
type CertManager struct {
	cfg      *config.Config
	c        collect.Collector
	db       *store.DB
	services *ServiceManager
	// scanner is used only by RenewCertbot, to find any haproxy-style
	// combined PEM the last scan identified as a copy of the lineage being
	// renewed (model.RenewalInfo.Derived) — those need recombining with the
	// fresh certificate, since certbot itself never touches them.
	scanner *inventory.Scanner
}

// NewCertManager builds the certificate issuer. services and scanner are
// used only by RenewCertbot: services to stop and restart nginx/haproxy
// around a --standalone renewal, scanner to find haproxy combined-PEM copies
// that need recombining afterward.
func NewCertManager(cfg *config.Config, c collect.Collector, db *store.DB,
	services *ServiceManager, scanner *inventory.Scanner) *CertManager {
	return &CertManager{cfg: cfg, c: c, db: db, services: services, scanner: scanner}
}

// Bounds for SelfSignedRequest.
const (
	defaultKeyBits = 2048
	defaultDays    = 397 // ~13 months, in line with current browser guidance
	maxDays        = 825 // the old CA/Browser Forum public-cert ceiling; sane even unenforced
)

// SelfSignedRequest describes the certificate to issue.
type SelfSignedRequest struct {
	Names   []string `json:"names"`
	Service string   `json:"service"` // nginx | haproxy
	Bits    int      `json:"bits"`    // 2048 | 3072 | 4096, default 2048
	Days    int      `json:"days"`    // 1..825, default 397
}

// hostnameRe accepts ordinary DNS labels; a literal leading "*." is allowed
// separately for wildcard names.
var hostnameRe = regexp.MustCompile(
	`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// normalise validates the request and fills in defaults. It never mutates the
// caller's slice in place.
func (r SelfSignedRequest) normalise() (SelfSignedRequest, error) {
	out := r
	if len(out.Names) == 0 {
		return out, fmt.Errorf("нужно указать хотя бы одно имя")
	}
	names := make([]string, len(out.Names))
	for i, n := range out.Names {
		n = strings.ToLower(strings.TrimSpace(n))
		bare := strings.TrimPrefix(n, "*.")
		if n == "" || !hostnameRe.MatchString(bare) {
			return out, fmt.Errorf("недопустимое имя: %q", out.Names[i])
		}
		names[i] = n
	}
	out.Names = names

	switch out.Service {
	case "nginx", "haproxy":
	case "":
		return out, fmt.Errorf("укажите service: nginx или haproxy")
	default:
		return out, fmt.Errorf("service должен быть nginx или haproxy, получено %q", out.Service)
	}

	if out.Bits == 0 {
		out.Bits = defaultKeyBits
	}
	switch out.Bits {
	case 2048, 3072, 4096:
	default:
		return out, fmt.Errorf("bits должен быть 2048, 3072 или 4096, получено %d", out.Bits)
	}

	if out.Days == 0 {
		out.Days = defaultDays
	}
	if out.Days < 1 || out.Days > maxDays {
		return out, fmt.Errorf("days должен быть от 1 до %d, получено %d", maxDays, out.Days)
	}
	return out, nil
}

// SelfSignedResult is what got written and how to wire it into a config.
type SelfSignedResult struct {
	Names        []string  `json:"names"`
	CertPath     string    `json:"cert_path,omitempty"`     // nginx: certificate
	KeyPath      string    `json:"key_path,omitempty"`      // nginx: private key
	CombinedPath string    `json:"combined_path,omitempty"` // haproxy: certificate + key in one file
	Fingerprint  string    `json:"fingerprint"`
	NotAfter     time.Time `json:"not_after"`
	// Snippet is the configuration to paste through the config editor. This
	// tool does not insert it itself.
	Snippet string `json:"snippet"`
}

// GenerateSelfSigned issues a self-signed certificate and writes it under a
// dedicated directory inside the target service's config root, so the write
// stays within the boundary ConfigManager already enforces for edits.
//
// A self-signed certificate still shows a browser warning — it is a stopgap
// for internal services or a quick test, not a replacement for a certificate
// from a trusted issuer.
func (m *CertManager) GenerateSelfSigned(ctx context.Context, user string, req SelfSignedRequest) (SelfSignedResult, error) {
	req, err := req.normalise()
	if err != nil {
		return SelfSignedResult{}, err
	}

	key, err := rsa.GenerateKey(rand.Reader, req.Bits)
	if err != nil {
		return SelfSignedResult{}, fmt.Errorf("генерация ключа: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return SelfSignedResult{}, fmt.Errorf("генерация серийного номера: %w", err)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: req.Names[0]},
		DNSNames:     req.Names,
		// A few minutes of backdating tolerates clock skew between this host
		// and whatever machine first connects.
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(0, 0, req.Days),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return SelfSignedResult{}, fmt.Errorf("создание сертификата: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return SelfSignedResult{}, fmt.Errorf("сериализация ключа: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	sum := sha256.Sum256(der)
	res := SelfSignedResult{
		Names:       req.Names,
		Fingerprint: hex.EncodeToString(sum[:]),
		NotAfter:    tmpl.NotAfter.UTC(),
	}

	root := m.cfg.NginxRoot
	if req.Service == "haproxy" {
		root = m.cfg.HAProxyRoot
	}
	dir := gopath.Join(root, "ssl-selfsigned", safeDirName(req.Names[0]))

	switch req.Service {
	case "nginx":
		res.CertPath = gopath.Join(dir, "fullchain.pem")
		res.KeyPath = gopath.Join(dir, "privkey.pem")
		if err := m.c.WriteFile(res.CertPath, certPEM, 0o644); err != nil {
			return SelfSignedResult{}, fmt.Errorf("запись сертификата: %w", err)
		}
		if err := m.c.WriteFile(res.KeyPath, keyPEM, 0o600); err != nil {
			return SelfSignedResult{}, fmt.Errorf("запись ключа: %w", err)
		}
		res.Snippet = fmt.Sprintf("ssl_certificate     %s;\nssl_certificate_key %s;", res.CertPath, res.KeyPath)

	case "haproxy":
		// haproxy's `crt` directive expects one PEM file containing the leaf
		// certificate followed by its private key.
		res.CombinedPath = gopath.Join(dir, "combined.pem")
		combined := make([]byte, 0, len(certPEM)+len(keyPEM))
		combined = append(combined, certPEM...)
		combined = append(combined, keyPEM...)
		if err := m.c.WriteFile(res.CombinedPath, combined, 0o600); err != nil {
			return SelfSignedResult{}, fmt.Errorf("запись сертификата: %w", err)
		}
		res.Snippet = fmt.Sprintf("bind *:443 ssl crt %s", res.CombinedPath)
	}

	m.db.Audit(ctx, user, "cert.generate_selfsigned", req.Names[0], "ok", map[string]any{
		"names": req.Names, "service": req.Service, "bits": req.Bits, "days": req.Days,
		"cert_path": res.CertPath, "key_path": res.KeyPath, "combined_path": res.CombinedPath,
	})
	return res, nil
}

// safeDirName keeps a hostname (including a leading "*.") usable as one path
// segment.
func safeDirName(name string) string {
	return strings.NewReplacer("*", "_wildcard_", "/", "_").Replace(name)
}

// lineageRe accepts certbot's own lineage directory names: the first domain a
// certificate was issued for, occasionally suffixed "-0001" and so on when
// certbot had to disambiguate a repeat request.
var lineageRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,253}[A-Za-z0-9])?$`)

// standaloneServices is what certbot's --standalone authenticator needs off
// port 80/443 for the duration of a renewal — a fixed list rather than
// something derived from what happens to be running, since guessing wrong
// about which service holds the port is worse than stopping one that was
// never listening.
var standaloneServices = []string{model.ServiceNginx, model.ServiceHAProxy}

// RenewCertbot re-issues a certbot-managed lineage in place.
//
// certbot itself only ever rewrites the files under
// /etc/letsencrypt/live/<lineage>/ that nginx (and, for a plain nginx-style
// setup, haproxy too) already reference directly — nothing else to change
// there. But haproxy needs certificate and key in one PEM, so a lineage that
// also feeds haproxy often has a deploy-hook-built combined copy elsewhere
// (model.RenewalInfo.Derived, see markDerivedCertbotCerts); that copy is
// invisible to certbot and is recombined here from the freshly renewed
// fullchain.pem+privkey.pem, then the owning service is reloaded to pick it
// up. This only runs for lineages the app already found a renewal.conf for
// (RenewalInfo.Managed) — an orphan lineage or a path outside
// /etc/letsencrypt is not something certbot can renew at all.
//
// A lineage authenticated via --standalone needs :80/:443 free while certbot
// itself talks to Let's Encrypt, so nginx and haproxy are stopped first and
// always restarted afterward regardless of how the renewal went — leaving
// the site down because a renewal failed is worse than the certificate
// simply staying as it was. When that happens the restart alone already
// picks up a recombined file, so no separate reload is needed on that path.
func (m *CertManager) RenewCertbot(ctx context.Context, user, lineage string) (collect.CommandResult, error) {
	if !lineageRe.MatchString(lineage) {
		return collect.CommandResult{}, fmt.Errorf("недопустимое имя lineage certbot: %q", lineage)
	}

	standalone := m.usesStandaloneAuth(lineage)
	if standalone {
		stopped, err := m.stopForStandalone(ctx, user)
		defer m.restartAfterStandalone(user, stopped)
		if err != nil {
			return collect.CommandResult{}, fmt.Errorf(
				"продление %s аутентифицируется через --standalone и требует остановки nginx/haproxy: %w",
				lineage, err)
		}
	}

	res, err := m.runCertbotRenew(ctx, user, lineage)
	if err != nil {
		return res, err
	}

	reloadTargets, err := m.recombineDerivedCerts(ctx, user, lineage)
	if err != nil {
		return res, fmt.Errorf(
			"сертификат %s продлён, но не удалось пересобрать копию для haproxy: %w", lineage, err)
	}
	if !standalone {
		// A --standalone renewal already restarts every stopped service with
		// the recombined file already in place; otherwise a service serving a
		// just-rewritten combined file needs reloading to pick it up — the
		// same gap the tls-cert-not-reloaded finding watches for.
		for _, svc := range reloadTargets {
			if _, err := m.services.Action(ctx, user, svc, "reload"); err != nil {
				return res, fmt.Errorf(
					"сертификат %s продлён и пересобран, но не удалось перечитать конфигурацию %s: %w",
					lineage, svc, err)
			}
		}
	}

	return res, nil
}

// recombineDerivedCerts rewrites every haproxy-style combined PEM the last
// scan identified as a byte-identical copy of lineage's certificate
// (model.RenewalInfo.Derived) with the freshly renewed certificate and key,
// and returns the distinct services that own one — for the caller to reload.
// certbot itself never touches these files: they live outside
// /etc/letsencrypt entirely, so without this step they would keep serving
// the certificate that was just renewed away from.
func (m *CertManager) recombineDerivedCerts(ctx context.Context, user, lineage string) ([]string, error) {
	snap := m.scanner.Latest()
	if snap == nil {
		return nil, nil
	}

	var certPEM, keyPEM []byte
	var services []string
	seen := map[string]bool{}
	for _, cert := range snap.Certs {
		if !cert.Renewal.Derived || cert.Renewal.Lineage != lineage {
			continue
		}
		if certPEM == nil {
			var err error
			certPEM, err = m.c.ReadFile(parse.LetsEncryptLive + lineage + "/fullchain.pem")
			if err != nil {
				return nil, fmt.Errorf("чтение обновлённого сертификата: %w", err)
			}
			keyPEM, err = m.c.ReadFile(parse.LetsEncryptLive + lineage + "/privkey.pem")
			if err != nil {
				return nil, fmt.Errorf("чтение обновлённого ключа: %w", err)
			}
		}

		combined := make([]byte, 0, len(certPEM)+len(keyPEM))
		combined = append(combined, certPEM...)
		combined = append(combined, keyPEM...)
		if err := m.c.WriteFile(cert.Path, combined, 0o600); err != nil {
			return services, fmt.Errorf("запись %s: %w", cert.Path, err)
		}
		m.db.Audit(ctx, user, "cert.recombine", cert.Path, "ok", map[string]any{"lineage": lineage})

		if !seen[cert.Service] {
			seen[cert.Service] = true
			services = append(services, cert.Service)
		}
	}
	return services, nil
}

// LineageInfo describes one certbot lineage found under /etc/letsencrypt/live.
type LineageInfo struct {
	Name string `json:"name"`
	// Known is false when fullchain.pem could not be read or parsed — the
	// lineage is still listed (it is still a valid combine target), just
	// without an expiry to show. NotAfter/DaysLeft are meaningless when
	// Known is false (DaysLeft == 0 is itself a valid "expires today").
	Known    bool      `json:"known"`
	NotAfter time.Time `json:"not_after,omitzero"`
	DaysLeft int       `json:"days_left"`
}

// ListLetsEncryptLineages lists the certbot lineages found directly under
// /etc/letsencrypt/live — the directory certbot itself maintains, so listing
// it is more reliable than trusting whatever this app happened to parse out
// of nginx/haproxy configuration — along with each one's expiry, so picking
// one to combine doesn't require checking the certificates table first.
func (m *CertManager) ListLetsEncryptLineages() ([]LineageInfo, error) {
	entries, err := m.c.ListDir(strings.TrimSuffix(parse.LetsEncryptLive, "/"))
	if err != nil {
		return nil, err
	}
	out := make([]LineageInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir {
			continue
		}
		info := LineageInfo{Name: gopath.Base(e.Path)}
		if raw, err := m.c.ReadFile(parse.LetsEncryptLive + info.Name + "/fullchain.pem"); err == nil {
			if block, _ := pem.Decode(raw); block != nil && block.Type == "CERTIFICATE" {
				if leaf, err := x509.ParseCertificate(block.Bytes); err == nil {
					info.Known = true
					info.NotAfter = leaf.NotAfter.UTC()
					info.DaysLeft = int(time.Until(leaf.NotAfter).Hours() / 24)
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CombineResult is what got written when packaging a certbot lineage for
// haproxy, and how to wire it in.
type CombineResult struct {
	Lineage      string    `json:"lineage"`
	CombinedPath string    `json:"combined_path"`
	Fingerprint  string    `json:"fingerprint"`
	NotAfter     time.Time `json:"not_after"`
	// Snippet is the configuration to paste through the config editor,
	// non-empty only when CombinedPath is a brand-new file nothing
	// references yet. When targetPath overwrote a file haproxy already uses,
	// there is nothing to paste — the config already points at it.
	Snippet string `json:"snippet,omitempty"`
}

// ListHAProxyCertPaths returns the haproxy certificate file paths the last
// scan actually found and read successfully — the exact rows "Подробности"
// already lists — so CombineForHAProxy can overwrite one of them by name
// instead of guessing where haproxy's config expects the file.
func (m *CertManager) ListHAProxyCertPaths() []string {
	snap := m.scanner.Latest()
	if snap == nil {
		return nil
	}
	var paths []string
	for _, cert := range snap.Certs {
		if cert.Service == model.ServiceHAProxy && cert.Error == "" {
			paths = append(paths, cert.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

func (m *CertManager) isKnownHAProxyCertPath(path string) bool {
	for _, p := range m.ListHAProxyCertPaths() {
		if p == path {
			return true
		}
	}
	return false
}

// CombineForHAProxy packages an already-issued certbot lineage's certificate
// and key into the single PEM file haproxy's "crt" directive expects.
//
// With targetPath set to one of ListHAProxyCertPaths()'s entries, it
// overwrites that exact file in place — the same path "Подробности" already
// shows haproxy using — and reloads haproxy, so the operator has nothing
// left to wire up by hand. With targetPath empty (no matching file exists
// yet, e.g. haproxy has never used this lineage before), it instead writes a
// brand-new file under a dedicated directory — the same write boundary
// GenerateSelfSigned uses — and returns the directive to paste through the
// validated config editor, since there is nothing yet to safely overwrite.
func (m *CertManager) CombineForHAProxy(ctx context.Context, user, lineage, targetPath string) (CombineResult, error) {
	if !lineageRe.MatchString(lineage) {
		return CombineResult{}, fmt.Errorf("недопустимое имя lineage certbot: %q", lineage)
	}
	if targetPath != "" && !m.isKnownHAProxyCertPath(targetPath) {
		return CombineResult{}, fmt.Errorf(
			"путь %q не найден среди текущих сертификатов haproxy — обновите страницу и выберите заново",
			targetPath)
	}

	certPEM, err := m.c.ReadFile(parse.LetsEncryptLive + lineage + "/fullchain.pem")
	if err != nil {
		return CombineResult{}, fmt.Errorf("чтение сертификата lineage %s: %w", lineage, err)
	}
	keyPEM, err := m.c.ReadFile(parse.LetsEncryptLive + lineage + "/privkey.pem")
	if err != nil {
		return CombineResult{}, fmt.Errorf("чтение ключа lineage %s: %w", lineage, err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return CombineResult{}, fmt.Errorf("lineage %s: fullchain.pem не в формате PEM", lineage)
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return CombineResult{}, fmt.Errorf("lineage %s: разбор сертификата: %w", lineage, err)
	}

	combined := make([]byte, 0, len(certPEM)+len(keyPEM))
	combined = append(combined, certPEM...)
	combined = append(combined, keyPEM...)

	sum := sha256.Sum256(leaf.Raw)
	res := CombineResult{
		Lineage:     lineage,
		Fingerprint: hex.EncodeToString(sum[:]),
		NotAfter:    leaf.NotAfter.UTC(),
	}

	if targetPath != "" {
		if err := m.c.WriteFile(targetPath, combined, 0o600); err != nil {
			return CombineResult{}, fmt.Errorf("запись %s: %w", targetPath, err)
		}
		res.CombinedPath = targetPath
		m.db.Audit(ctx, user, "cert.combine_haproxy", targetPath, "ok", map[string]any{
			"lineage": lineage, "fingerprint": res.Fingerprint,
		})
		if _, err := m.services.Action(ctx, user, model.ServiceHAProxy, "reload"); err != nil {
			return res, fmt.Errorf("PEM собран и записан в %s, но не удалось перечитать конфигурацию haproxy: %w",
				targetPath, err)
		}
		return res, nil
	}

	dir := gopath.Join(m.cfg.HAProxyRoot, "ssl-letsencrypt", safeDirName(lineage))
	res.CombinedPath = gopath.Join(dir, "combined.pem")
	if err := m.c.WriteFile(res.CombinedPath, combined, 0o600); err != nil {
		return CombineResult{}, fmt.Errorf("запись %s: %w", res.CombinedPath, err)
	}
	res.Snippet = fmt.Sprintf("bind *:443 ssl crt %s", res.CombinedPath)

	m.db.Audit(ctx, user, "cert.combine_haproxy", res.CombinedPath, "ok", map[string]any{
		"lineage": lineage, "fingerprint": res.Fingerprint,
	})
	return res, nil
}

// usesStandaloneAuth reads the lineage's own renewal.conf and reports
// whether certbot will bind the ports itself rather than going through a
// webroot, an installer plugin, or DNS. Any error or unrecognised layout is
// treated as "no" — the safe default leaves nginx/haproxy exactly as they
// were, since most lineages need no service interruption at all.
func (m *CertManager) usesStandaloneAuth(lineage string) bool {
	raw, err := m.c.ReadFile("/etc/letsencrypt/renewal/" + lineage + ".conf")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) == "authenticator" {
			return strings.TrimSpace(value) == "standalone"
		}
	}
	return false
}

// stopForStandalone stops nginx and haproxy in order, returning exactly the
// services it actually managed to stop even when it returns an error — the
// caller must only restart what this function actually took down.
func (m *CertManager) stopForStandalone(ctx context.Context, user string) ([]string, error) {
	stopped := make([]string, 0, len(standaloneServices))
	for _, svc := range standaloneServices {
		if _, err := m.services.Action(ctx, user, svc, "stop"); err != nil {
			return stopped, fmt.Errorf("остановка %s: %w", svc, err)
		}
		stopped = append(stopped, svc)
	}
	return stopped, nil
}

// restartAfterStandalone starts every service stopForStandalone stopped. It
// runs on its own timeout independent of the request context: a client
// disconnecting or an HTTP timeout must never be the reason nginx/haproxy
// stay down.
func (m *CertManager) restartAfterStandalone(user string, stopped []string) {
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.CommandTimeout)
	defer cancel()
	for _, svc := range stopped {
		if _, err := m.services.Action(ctx, user, svc, "start"); err != nil {
			m.db.Audit(ctx, user, "cert.renew_restart_failed", svc, "error", err.Error())
		}
	}
}

// runCertbotRenew is the actual `certbot renew` invocation, shared by both
// the standalone and non-standalone paths.
func (m *CertManager) runCertbotRenew(ctx context.Context, user, lineage string) (collect.CommandResult, error) {
	res, err := m.c.Run(ctx, "certbot", "renew", "--cert-name", lineage, "--non-interactive")
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "cert.renew", lineage, outcome, map[string]any{
		"exit_code": res.ExitCode, "output": strings.TrimSpace(res.Output()), "simulated": res.Simulated,
	})
	if err != nil {
		return res, err
	}
	if !res.OK() {
		return res, fmt.Errorf("certbot renew --cert-name %s: код %d: %s", lineage, res.ExitCode,
			strings.TrimSpace(res.Output()))
	}
	return res, nil
}
