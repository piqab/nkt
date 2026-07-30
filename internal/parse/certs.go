package parse

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	gopath "path"
	"sort"
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// CertResult is everything the certificate reader produces.
type CertResult struct {
	Status model.SourceStatus
	Certs  []model.Certificate
}

// letsEncryptLive is where certbot keeps the symlinks a config normally points at.
const letsEncryptLive = "/etc/letsencrypt/live/"

// certUsage tracks which endpoints and site names a certificate path serves.
// One file (or, for haproxy's directory form, one directory) often serves
// several endpoints.
type certUsage struct {
	service   string
	endpoints []string
	sites     []string
}

// Certificates reads every certificate the parsed endpoints refer to.
//
// The file on disk is the source of truth here rather than the socket: a
// certificate can be expired long before anything connects to the port, and the
// whole point is to notice that in advance.
func Certificates(ctx context.Context, c collect.Collector, endpoints []model.Endpoint) CertResult {
	started := time.Now()
	res := CertResult{Status: model.SourceStatus{Name: "certificates"}}
	defer func() { res.Status.DurationMS = time.Since(started).Milliseconds() }()

	paths := map[string]*certUsage{}
	var order []string

	for _, e := range endpoints {
		certPath := e.Extra["ssl_certificate"]
		if certPath == "" {
			continue
		}
		u, ok := paths[certPath]
		if !ok {
			u = &certUsage{service: e.Service}
			paths[certPath] = u
			order = append(order, certPath)
		}
		u.endpoints = appendUnique(u.endpoints, e.Socket())
		for _, name := range e.Names {
			if isHostname(name) {
				u.sites = appendUnique(u.sites, name)
			}
		}
	}
	if len(order) == 0 {
		res.Status.Available = true
		return res
	}
	res.Status.Available = true

	renewals := discoverRenewals(ctx, c)
	sort.Strings(order)

	for _, path := range order {
		u := paths[path]

		// haproxy's "bind ... crt" can name a directory instead of a single PEM
		// file: it then picks a certificate per connection by SNI. Every bundle
		// in the directory is a certificate in its own right and gets checked
		// individually — there is no single file to read at the directory path.
		if info, err := c.Stat(path); err == nil && info.IsDir {
			files, err := certDirEntries(c, path)
			if err != nil {
				res.Certs = append(res.Certs, dirError(&res, path, u, renewals,
					fmt.Sprintf("каталог сертификатов недоступен: %v", err)))
				continue
			}
			if len(files) == 0 {
				res.Certs = append(res.Certs, dirError(&res, path, u, renewals, "каталог сертификатов пуст"))
				continue
			}
			for _, file := range files {
				res.Certs = append(res.Certs, readCertFile(&res, c, file, u, renewals))
			}
			continue
		}

		res.Certs = append(res.Certs, readCertFile(&res, c, path, u, renewals))
	}

	markDerivedCertbotCerts(res.Certs)

	sort.Slice(res.Certs, func(i, j int) bool {
		if res.Certs[i].Error != res.Certs[j].Error {
			return res.Certs[i].Error != "" // unreadable first, they need attention
		}
		return res.Certs[i].DaysLeft < res.Certs[j].DaysLeft
	})
	res.Status.Files = certPaths(res.Certs)
	return res
}

// dirError builds a placeholder certificate describing why a "crt" directory
// itself could not be resolved into any certificate bundle.
func dirError(res *CertResult, path string, u *certUsage, renewals renewalIndex, reason string) model.Certificate {
	cert := model.Certificate{
		ID:        "cert:" + path,
		Path:      path,
		Service:   u.service,
		Endpoints: u.endpoints,
		Sites:     u.sites,
		Renewal:   renewals.forPath(path),
		Error:     reason,
	}
	res.Status.Warnings = append(res.Status.Warnings, path+": "+reason)
	return cert
}

// readCertFile reads and parses a single certificate file, recording any
// failure both on the returned certificate and in the source status.
func readCertFile(res *CertResult, c collect.Collector, path string, u *certUsage, renewals renewalIndex) model.Certificate {
	cert := model.Certificate{
		ID:        "cert:" + path,
		Path:      path,
		Service:   u.service,
		Endpoints: u.endpoints,
		Sites:     u.sites,
		Renewal:   renewals.forPath(path),
	}

	raw, err := c.ReadFile(path)
	if err != nil {
		cert.Error = fmt.Sprintf("файл недоступен: %v", err)
		res.Status.Warnings = append(res.Status.Warnings, path+": "+cert.Error)
		return cert
	}
	if err := fillFromPEM(&cert, raw); err != nil {
		cert.Error = err.Error()
		res.Status.Warnings = append(res.Status.Warnings, path+": "+cert.Error)
	}
	return cert
}

// certDirEntries lists the certificate bundles inside a haproxy "crt"
// directory, skipping the private-key/OCSP/issuer companion files haproxy
// expects alongside each bundle and any hidden files.
func certDirEntries(c collect.Collector, dir string) ([]string, error) {
	entries, err := c.ListDir(dir)
	if err != nil {
		return nil, err
	}
	companionSuffixes := []string{".key", ".ocsp", ".issuer", ".sctl"}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		base := gopath.Base(e.Path)
		if strings.HasPrefix(base, ".") {
			continue
		}
		skip := false
		for _, suf := range companionSuffixes {
			if strings.HasSuffix(base, suf) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out, nil
}

func certPaths(certs []model.Certificate) []string {
	out := make([]string, 0, len(certs))
	for _, c := range certs {
		out = append(out, c.Path)
	}
	return out
}

// fillFromPEM decodes a PEM bundle and describes its leaf certificate.
func fillFromPEM(cert *model.Certificate, raw []byte) error {
	var parsed []*x509.Certificate
	rest := raw
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue // private keys and DH params live in these bundles too
		}
		x, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("разбор сертификата: %v", err)
		}
		parsed = append(parsed, x)
	}
	if len(parsed) == 0 {
		return fmt.Errorf("в файле нет ни одного сертификата в формате PEM")
	}

	leaf := parsed[0]
	cert.ChainLength = len(parsed)
	cert.Subject = leaf.Subject.String()
	cert.Issuer = leaf.Issuer.String()
	cert.Serial = leaf.SerialNumber.String()
	cert.NotBefore = leaf.NotBefore.UTC()
	cert.NotAfter = leaf.NotAfter.UTC()
	cert.DaysLeft = daysUntil(leaf.NotAfter)
	cert.SigAlgorithm = leaf.SignatureAlgorithm.String()
	cert.SelfSigned = leaf.Issuer.String() == leaf.Subject.String()
	sum := sha256.Sum256(leaf.Raw)
	cert.Fingerprint = hex.EncodeToString(sum[:])

	names := []string{}
	if leaf.Subject.CommonName != "" {
		names = append(names, leaf.Subject.CommonName)
	}
	for _, n := range leaf.DNSNames {
		names = appendUnique(names, n)
	}
	for _, ip := range leaf.IPAddresses {
		names = appendUnique(names, ip.String())
	}
	cert.Names = names

	switch pub := leaf.PublicKey.(type) {
	case *rsa.PublicKey:
		cert.KeyAlgorithm, cert.KeyBits = "RSA", pub.N.BitLen()
	case *ecdsa.PublicKey:
		cert.KeyAlgorithm, cert.KeyBits = "ECDSA", pub.Curve.Params().BitSize
	case ed25519.PublicKey:
		cert.KeyAlgorithm, cert.KeyBits = "Ed25519", 256
	default:
		cert.KeyAlgorithm = leaf.PublicKeyAlgorithm.String()
	}
	return nil
}

// daysUntil counts whole days remaining, negative once the moment has passed.
func daysUntil(t time.Time) int {
	return int(time.Until(t).Hours() / 24)
}

// isHostname filters out nginx's catch-all and regex server_name values, which
// are not names a certificate could ever be checked against.
func isHostname(name string) bool {
	if name == "" || name == "_" || strings.ContainsAny(name, "~^$\\") {
		return false
	}
	return strings.Contains(name, ".")
}

// --------------------------------------------------------------------- renewal

// renewalIndex holds what was discovered about certificate automation.
type renewalIndex struct {
	// lineages are the certbot lineage names found under /etc/letsencrypt/renewal.
	lineages map[string]bool
	// automatic reports whether a timer or cron job actually runs the renewal.
	automatic bool
	detail    string
	tool      string
}

// forPath decides how a specific certificate is renewed.
func (r renewalIndex) forPath(path string) model.RenewalInfo {
	if !strings.HasPrefix(path, letsEncryptLive) {
		return model.RenewalInfo{
			Tool:   "manual",
			Detail: "путь вне /etc/letsencrypt — обновление, скорее всего, ручное",
		}
	}

	lineage := strings.TrimPrefix(path, letsEncryptLive)
	if i := strings.IndexByte(lineage, '/'); i >= 0 {
		lineage = lineage[:i]
	}
	if !r.lineages[lineage] {
		return model.RenewalInfo{
			Tool:    "certbot",
			Lineage: lineage,
			Detail: fmt.Sprintf(
				"сертификат лежит в /etc/letsencrypt/live/%s, но файла обновления "+
					"/etc/letsencrypt/renewal/%s.conf нет — certbot его не продлит",
				lineage, lineage),
		}
	}
	return model.RenewalInfo{
		Tool:      firstNonEmpty(r.tool, "certbot"),
		Managed:   true,
		Automatic: r.automatic,
		Detail:    r.detail,
		Lineage:   lineage,
	}
}

// markDerivedCertbotCerts catches the common haproxy pattern: nginx gets its
// certificate straight from /etc/letsencrypt/live/<lineage>, but haproxy's
// "crt" wants certificate and key in one file, so a certbot deploy-hook
// concatenates fullchain.pem+privkey.pem into a copy living somewhere else.
// forPath sees only a path outside /etc/letsencrypt and calls that "manual",
// which is wrong: the certificate is certbot-issued, it just isn't the file
// certbot itself will touch on renewal. A copy has byte-identical leaf
// certificate content to its source, so the fingerprint already computed by
// fillFromPEM is enough to catch it — no assumption about hook script naming
// or location is needed.
func markDerivedCertbotCerts(certs []model.Certificate) {
	sources := map[string]model.Certificate{}
	for _, cert := range certs {
		if cert.Error != "" || cert.Fingerprint == "" || cert.Renewal.Lineage == "" {
			continue
		}
		if !strings.HasPrefix(cert.Path, letsEncryptLive) {
			continue
		}
		if _, ok := sources[cert.Fingerprint]; !ok {
			sources[cert.Fingerprint] = cert
		}
	}
	if len(sources) == 0 {
		return
	}

	for i := range certs {
		cert := &certs[i]
		if cert.Error != "" || cert.Fingerprint == "" {
			continue
		}
		if strings.HasPrefix(cert.Path, letsEncryptLive) {
			continue
		}
		src, ok := sources[cert.Fingerprint]
		if !ok {
			continue
		}

		detail := fmt.Sprintf(
			"тот же сертификат, что и %s — похоже, объединён с приватным ключом для другого "+
				"сервиса (типично для haproxy, которому certbot не пишет напрямую).", src.Path)
		if src.Renewal.Managed {
			detail += fmt.Sprintf(" certbot renew --cert-name %s продлит оригинал, но этот файл "+
				"нужно пересобрать отдельно — deploy-hook'ом certbot или вручную.", src.Renewal.Lineage)
		} else {
			detail += " " + src.Renewal.Detail
		}

		cert.Renewal = model.RenewalInfo{
			Tool:       src.Renewal.Tool,
			Managed:    src.Renewal.Managed,
			Automatic:  src.Renewal.Automatic,
			Lineage:    src.Renewal.Lineage,
			Derived:    true,
			SourcePath: src.Path,
			Detail:     detail,
		}
	}
}

// discoverRenewals looks for certificate automation on the host: which lineages
// certbot knows about, and whether anything actually triggers a renewal.
func discoverRenewals(ctx context.Context, c collect.Collector) renewalIndex {
	idx := renewalIndex{lineages: map[string]bool{}, tool: "certbot"}

	if files, err := c.Glob("/etc/letsencrypt/renewal/*.conf"); err == nil {
		for _, f := range files {
			idx.lineages[strings.TrimSuffix(gopath.Base(f), ".conf")] = true
		}
	}
	if len(idx.lineages) == 0 {
		return idx
	}

	// A renewal file without something to run it is just a file.
	for _, unit := range []string{"certbot.timer", "snap.certbot.renew.timer"} {
		res, err := c.Run(ctx, "systemctl", "is-active", unit)
		if err == nil && strings.TrimSpace(res.Stdout) == "active" {
			idx.automatic = true
			idx.detail = fmt.Sprintf("автообновление включено: таймер %s активен", unit)
			return idx
		}
	}
	for _, cron := range []string{"/etc/cron.d/certbot", "/etc/cron.daily/certbot"} {
		if c.Exists(cron) {
			idx.automatic = true
			idx.detail = "автообновление включено: задание " + cron
			return idx
		}
	}
	idx.detail = "certbot знает о сертификате, но ни таймер certbot.timer, " +
		"ни задание cron не найдены — продление не запустится само"
	return idx
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
