package inventory

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/althq/netknownsthat/internal/model"
	"github.com/althq/netknownsthat/internal/tlscheck"
)

// maxParallelCertChecks bounds how many live TLS dials run at once during a
// scan. The certificate count on one host is small, so this is generous
// headroom rather than a real throttle.
const maxParallelCertChecks = 8

// checkLiveCertificates dials the first known endpoint of every certificate
// and records what is actually served. A file on disk that parses cleanly and
// has months of validity left is not the same claim as "this is what clients
// receive": certbot can renew the file while nothing reloads the service that
// serves it, and the only way to catch that is to look at the socket.
func checkLiveCertificates(ctx context.Context, certs []model.Certificate, endpoints []model.Endpoint, timeout time.Duration) {
	byPath := map[string][]model.Endpoint{}
	for _, e := range endpoints {
		if p := e.Extra["ssl_certificate"]; p != "" {
			byPath[p] = append(byPath[p], e)
		}
	}

	sem := make(chan struct{}, maxParallelCertChecks)
	var wg sync.WaitGroup
	for i := range certs {
		cert := &certs[i]
		eps := byPath[cert.Path]
		if cert.Error != "" || len(eps) == 0 {
			continue
		}
		wg.Add(1)
		go func(cert *model.Certificate, ep model.Endpoint) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			verifyServing(ctx, cert, ep, timeout)
		}(cert, eps[0])
	}
	wg.Wait()
}

// verifyServing checks one certificate against the socket it is configured on.
func verifyServing(ctx context.Context, cert *model.Certificate, ep model.Endpoint, timeout time.Duration) {
	addr := net.JoinHostPort(probeHost(ep.Address), strconv.Itoa(ep.Port))
	serverName := ""
	switch {
	case len(cert.Sites) > 0:
		serverName = cert.Sites[0]
	case len(cert.Names) > 0:
		serverName = cert.Names[0]
	}

	cert.Serving.Checked = true
	cert.Serving.Endpoint = addr

	served, err := tlscheck.Fetch(ctx, addr, serverName, timeout)
	if err != nil {
		cert.Serving.Error = err.Error()
		return
	}
	cert.Serving.ServedSerial = served.Serial
	cert.Serving.ServedNotAfter = served.NotAfter
	cert.Serving.Match = served.Fingerprint == cert.Fingerprint
}
