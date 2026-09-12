package api

import (
	"context"
	"github.com/piqab/nkt/internal/msgs"
	"net"
	"net/http"
	"strings"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/portprobe"
)

// handlePortProbe проверяет порт с самого хоста.
//
// Только свои адреса: loopback и адреса интерфейсов из последнего
// сканирования. Проверка — это соединение изнутри хоста, и разрешить ей
// любой адрес значило бы превратить nkt в сканер чужих сетей от имени
// хоста.
func (s *Server) handlePortProbe(w http.ResponseWriter, r *http.Request) {
	var req portprobe.Request
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.Kind == portprobe.KindCurl {
		// Настоящий curl с аргументами оператора — через ту же
		// песочницу, что и остальные команды хоста; без оболочки.
		if !collect.Which(r.Context(), s.scanner.Collector(), "curl") {
			writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "api.curlInstalledHost"))
			return
		}
		run := func(ctx context.Context, argv ...string) (collect.CommandResult, error) {
			return s.scanner.Collector().Run(ctx, argv[0], argv[1:]...)
		}
		writeJSON(w, http.StatusOK, portprobe.RunCurl(r.Context(), run, req))
		return
	}
	if !s.isOwnAddress(r, req.Address) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "api.onlyHostSOwnAddresses"))
		return
	}
	writeJSON(w, http.StatusOK, portprobe.Probe(r.Context(), req))
}

// isOwnAddress отвечает, принадлежит ли адрес этому хосту.
func (s *Server) isOwnAddress(r *http.Request, addr string) bool {
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	snap, err := s.scanner.LatestOrScan(r.Context())
	if err != nil {
		return false
	}
	for _, iface := range snap.Interfaces {
		for _, cidr := range iface.Addresses {
			own := strings.TrimSpace(cidr)
			if i := strings.Index(own, "/"); i > 0 {
				own = own[:i]
			}
			if net.ParseIP(own) != nil && net.ParseIP(own).Equal(ip) {
				return true
			}
		}
	}
	return false
}
