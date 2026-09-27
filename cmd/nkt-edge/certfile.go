package main

import (
	"crypto/tls"
	"os"
	"sync"
	"time"
)

// certFile — сертификат из файлов (их кладёт туда deploy-hook certbot).
// После продления файлы меняются — сертификат перечитывается при
// следующем рукопожатии, без перезапуска службы и без обрыва туннеля.
type certFile struct {
	certPath, keyPath string

	mu      sync.Mutex
	cert    *tls.Certificate
	modTime time.Time
	checked time.Time
}

// load — первая загрузка: без сертификата служба не стартует.
func (c *certFile) load() error {
	_, err := c.get(time.Now())
	return err
}

func (c *certFile) get(now time.Time) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cert != nil && now.Sub(c.checked) < time.Minute {
		return c.cert, nil
	}
	c.checked = now
	st, err := os.Stat(c.certPath)
	if err != nil {
		if c.cert != nil {
			return c.cert, nil
		}
		return nil, err
	}
	if c.cert != nil && !st.ModTime().After(c.modTime) {
		return c.cert, nil
	}
	cert, err := tls.LoadX509KeyPair(c.certPath, c.keyPath)
	if err != nil {
		if c.cert != nil {
			// Файлы меняются прямо сейчас — отдаём прежний.
			return c.cert, nil
		}
		return nil, err
	}
	c.cert, c.modTime = &cert, st.ModTime()
	return c.cert, nil
}

func (c *certFile) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	return c.get(time.Now())
}
