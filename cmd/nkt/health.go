package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// runHealth — `nkt health`: отвечает ли служба на этой машине (проверка
// здоровья контейнера). Порт — из NKT_ADDR; хаб с NKT_TLS_ENABLED слушает
// HTTPS со своим самоподписанным сертификатом — пробуются оба, сертификат
// не проверяется: спрашиваем сами себя по 127.0.0.1.
func runHealth() error {
	addr := os.Getenv("NKT_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8077"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8077"
	}
	client := &http.Client{
		Timeout: 4 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- проверка самого себя на 127.0.0.1
		},
	}
	schemes := []string{"http", "https"}
	if v := strings.ToLower(os.Getenv("NKT_TLS_ENABLED")); v == "true" || v == "1" || v == "yes" {
		schemes = []string{"https", "http"}
	}
	var last error
	for _, sc := range schemes {
		resp, err := client.Get(sc + "://127.0.0.1:" + port + "/api/health")
		if err != nil {
			last = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return nil
		}
		last = fmt.Errorf("%s: HTTP %d", sc, resp.StatusCode)
	}
	return fmt.Errorf("health: %v", last)
}
