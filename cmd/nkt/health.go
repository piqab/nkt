package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runHealth — `nkt health`: отвечает ли служба на этой машине (проверка
// здоровья контейнера). Порт — из NKT_ADDR. С NKT_TLS_ENABLED хаб слушает
// HTTPS: проверка доверяет ровно его сертификату — NKT_TLS_CERT или
// самоподписанному из каталога данных, — а не отключает проверку.
func runHealth() error {
	addr := os.Getenv("NKT_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8077"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8077"
	}
	client := &http.Client{Timeout: 4 * time.Second}
	url := "http://127.0.0.1:" + port + "/api/health"
	if v := strings.ToLower(os.Getenv("NKT_TLS_ENABLED")); v == "true" || v == "1" || v == "yes" {
		cfg, err := healthTLS()
		if err != nil {
			return fmt.Errorf("health: %v", err)
		}
		client.Transport = &http.Transport{TLSClientConfig: cfg}
		url = "https://127.0.0.1:" + port + "/api/health"
	}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: HTTP %d", resp.StatusCode)
	}
	return nil
}

// healthTLS — доверие только сертификату самого nkt: корни — его файл,
// имя для проверки — 127.0.0.1, если он на него выписан, иначе первое имя
// из сертификата (соединение всё равно идёт на 127.0.0.1).
func healthTLS() (*tls.Config, error) {
	certFile := os.Getenv("NKT_TLS_CERT")
	if certFile == "" {
		dataDir := os.Getenv("NKT_DATA_DIR")
		if dataDir == "" {
			dataDir = "/var/lib/netknownsthat"
		}
		certFile = filepath.Join(dataDir, "tls", "cert.pem")
	}
	raw, err := os.ReadFile(certFile) // #nosec G304 -- путь из настроек самого nkt
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, fmt.Errorf("%s: no certificate", certFile)
	}
	name := "127.0.0.1"
	if block, _ := pem.Decode(raw); block != nil {
		if leaf, err := x509.ParseCertificate(block.Bytes); err == nil && leaf.VerifyHostname(name) != nil && len(leaf.DNSNames) > 0 {
			name = leaf.DNSNames[0]
		}
	}
	return &tls.Config{RootCAs: pool, ServerName: name, MinVersion: tls.VersionTLS12}, nil
}
