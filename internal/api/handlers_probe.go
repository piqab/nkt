package api

import (
	"net"
	"net/http"
	"strings"

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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.isOwnAddress(r, req.Address) {
		writeError(w, http.StatusBadRequest, "проверять можно только адреса самого хоста: loopback и адреса его интерфейсов")
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
