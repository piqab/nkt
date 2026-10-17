package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/piqab/nkt/internal/fly"
)

// flyCerts — сертификаты Let's Encrypt для маршрутов с TLS на шлюзе:
// выпуск и продление (autocert, проверка HTTP-01 по порту 80 шлюза), ключи
// и сертификаты — в FLY_DIR/certs (0700, только root), с хаба не идут.
// Состояние по именам — в сводку для карты.
type flyCerts struct {
	m     *autocert.Manager
	mu    sync.Mutex
	state map[string]fly.CertStatus
}

func newFlyCerts(dir string, names func() []string) *flyCerts {
	certDir := filepath.Join(dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		log.Printf("fly certs: %v", err)
	}
	c := &flyCerts{state: map[string]fly.CertStatus{}}
	c.m = &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(certDir),
		// Только имена маршрутов с TLS на шлюзе: чужое имя в SNI не
		// заставит шлюз просить сертификат.
		HostPolicy: func(_ context.Context, host string) error {
			for _, n := range names() {
				if n == host {
					return nil
				}
			}
			return errors.New("fly: not a gateway TLS name")
		},
		Email: os.Getenv("FLY_ACME_EMAIL"),
	}
	// Для проверки на стенде — свой каталог ACME (pebble, staging).
	if u := os.Getenv("FLY_ACME_DIRECTORY"); u != "" {
		c.m.Client = &acme.Client{DirectoryURL: u}
	}
	return c
}

// GetCertificate — сертификат для рукопожатия; итог запоминается.
func (c *flyCerts) GetCertificate(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, err := c.m.GetCertificate(h)
	c.record(h.ServerName, cert, err)
	return cert, err
}

func (c *flyCerts) record(name string, cert *tls.Certificate, err error) {
	st := fly.CertStatus{Name: name}
	if err != nil {
		st.Error = err.Error()
	} else if cert != nil {
		leaf := cert.Leaf
		if leaf == nil && len(cert.Certificate) > 0 {
			leaf, _ = x509.ParseCertificate(cert.Certificate[0])
		}
		if leaf != nil {
			st.NotAfter = leaf.NotAfter.Unix()
		}
	}
	c.mu.Lock()
	c.state[name] = st
	c.mu.Unlock()
}

// Prefetch — получить сертификаты заранее, а не на первом посетителе: как
// у современного браузера (ECDSA), чтобы autocert не выпустил лишний RSA.
func (c *flyCerts) Prefetch(names []string) {
	for _, n := range names {
		go func(n string) {
			h := &tls.ClientHelloInfo{ServerName: n,
				CipherSuites:      []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_AES_128_GCM_SHA256},
				SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
				SupportedCurves:   []tls.CurveID{tls.CurveP256},
				SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12}}
			// Свой предел у autocert — 5 минут на выпуск.
			cert, err := c.m.GetCertificate(h)
			c.record(n, cert, err)
			if err != nil {
				log.Printf("fly certs: %s: %v", n, err)
			}
		}(n)
	}
}

// Status — состояние по именам маршрутов.
func (c *flyCerts) Status(names []string) []fly.CertStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []fly.CertStatus{}
	for _, n := range names {
		st, ok := c.state[n]
		if !ok {
			st = fly.CertStatus{Name: n, Pending: true}
		}
		out = append(out, st)
	}
	return out
}
