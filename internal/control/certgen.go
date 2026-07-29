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
	"strings"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/store"
)

// CertManager issues certificate material directly on the host.
//
// It never edits a live nginx or haproxy configuration. Locating the right
// server block and rewriting it safely is a much larger feature than issuing a
// certificate, and every config edit already goes through ConfigManager's
// validated, auto-rolled-back path — GenerateSelfSigned writes the new files
// and hands back the exact directives to paste there.
type CertManager struct {
	cfg *config.Config
	c   collect.Collector
	db  *store.DB
}

// NewCertManager builds the certificate issuer.
func NewCertManager(cfg *config.Config, c collect.Collector, db *store.DB) *CertManager {
	return &CertManager{cfg: cfg, c: c, db: db}
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
