package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/control"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// Порты хоста на машины (1.17.5): администратор задаёт у хоста пул портов
// (по умолчанию его нет), а владелец машины — администратор или токен
// «по правам» с действием «порты» — берёт из пула порт на свою машину.
// Адрес машины при этом закрепляется (бронь DHCP), проброс — правилами
// хоста (DNAT, TCP или UDP). Пул и выданные порты — по именам хоста и
// машины: так они переживают экспорт и импорт хаба.

const (
	portPoolKV  = "vm.portpool"
	portAllocKV = "vm.ports"
	// Служебные порты Traffic Fly на хостах — с 40001: пул их не задевает.
	portPoolFlyFrom = 40000
	portPoolFlyTo   = 41999
	portPoolMaxSize = 10000
)

// PortPool — пул портов хоста; From = 0 — пула нет.
type PortPool struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// PortAlloc — выданный порт: хост и порт на нём → машина и её порт.
type PortAlloc struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Proto  string `json:"proto"`
	VM     string `json:"vm"`
	VMPort int    `json:"vm_port"`
	// Token — имя токена, взявшего порт (пусто — администратор).
	Token string `json:"token,omitempty"`
}

var portsMu sync.Mutex

func (s *Server) portPools(ctx context.Context) map[string]PortPool {
	out := map[string]PortPool{}
	if raw, ok, _ := s.db.KVGet(ctx, portPoolKV); ok {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) portAllocs(ctx context.Context) []PortAlloc {
	var out []PortAlloc
	if raw, ok, _ := s.db.KVGet(ctx, portAllocKV); ok {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) savePortAllocs(ctx context.Context, list []PortAlloc) error {
	raw, _ := json.Marshal(list)
	return s.db.KVSet(ctx, portAllocKV, string(raw))
}

// parentName — имя хоста машины (машина хаба — localhost).
func (s *Server) parentName(ctx context.Context, vm store.Host) (string, error) {
	if vm.ParentID == localHostID {
		return localHostName, nil
	}
	p, err := s.db.HostByID(ctx, vm.ParentID)
	if err != nil {
		return "", err
	}
	return p.Name, nil
}

// handlePortPool — GET/PUT /hub/hosts/{id}/port-pool: пул хоста
// (администратор; {"from":20000,"to":20999}, from = 0 — выключить).
func (s *Server) handlePortPool(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := localHostName
	if ref := chi.URLParam(r, "id"); ref != "local" {
		id, err := strconv.ParseInt(ref, 10, 64)
		if err != nil {
			writeErr(w, r, http.StatusBadRequest, err)
			return
		}
		h, err := s.db.HostByID(ctx, id)
		if err != nil {
			fail(w, r, err)
			return
		}
		name = h.Name
	}
	portsMu.Lock()
	defer portsMu.Unlock()
	pools := s.portPools(ctx)
	if r.Method == http.MethodGet {
		used := 0
		for _, a := range s.portAllocs(ctx) {
			if a.Host == name {
				used++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"pool": pools[name], "used": used})
		return
	}
	var p PortPool
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if p.From != 0 && (p.From < 1024 || p.To < p.From || p.To > 65535 || p.To-p.From >= portPoolMaxSize ||
		p.From <= portPoolFlyTo && p.To >= portPoolFlyFrom) {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.portPoolBad", portPoolMaxSize, portPoolFlyFrom, portPoolFlyTo))
		return
	}
	// Выданные порты вне нового пула остаются, пока их не закроют.
	before := pools[name]
	if p.From == 0 {
		delete(pools, name)
	} else {
		pools[name] = p
	}
	raw, _ := json.Marshal(pools)
	if err := s.db.KVSet(ctx, portPoolKV, string(raw)); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, auth.Username(ctx), "vm.port_pool", name, "ok", map[string]any{"before": before, "after": p})
	writeJSON(w, http.StatusOK, map[string]any{"pool": p})
}

// handleVMPorts — GET /hub/hosts/{id}/vm/ports: порты машины.
func (s *Server) handleVMPorts(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	host, err := s.parentName(r.Context(), vm)
	if err != nil {
		fail(w, r, err)
		return
	}
	out := []PortAlloc{}
	for _, a := range s.portAllocs(r.Context()) {
		if a.Host == host && a.VM == vm.Name {
			out = append(out, a)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ports": out, "address": s.vmPublicAddr(r.Context(), vm)})
}

// vmPublicAddr — адрес хоста машины, по которому открыт порт.
func (s *Server) vmPublicAddr(ctx context.Context, vm store.Host) string {
	if vm.ParentID == localHostID {
		return ""
	}
	if p, err := s.db.HostByID(ctx, vm.ParentID); err == nil {
		return p.Addr
	}
	return ""
}

// handleVMPortOpen — POST /hub/hosts/{id}/vm/ports {"vm_port":22,"proto":"tcp"}:
// свободный порт пула хоста → порт машины.
func (s *Server) handleVMPortOpen(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	var req struct {
		VMPort int    `json:"vm_port"`
		Proto  string `json:"proto"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.Proto == "" {
		req.Proto = "tcp"
	}
	if req.VMPort < 1 || req.VMPort > 65535 || req.Proto != "tcp" && req.Proto != "udp" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("control.portForwardBadPort", req.VMPort))
		return
	}
	ctx := r.Context()
	host, err := s.parentName(ctx, vm)
	if err != nil {
		fail(w, r, err)
		return
	}
	portsMu.Lock()
	defer portsMu.Unlock()
	allocs := s.portAllocs(ctx)
	tokenName := ""
	if t, byToken := tokenFromContext(ctx); byToken && t.Limited() {
		tokenName = t.Name
		n := 0
		for _, a := range allocs {
			if a.Token == t.Name {
				n++
			}
		}
		if n >= t.Perms.Limits.Ports {
			writeErr(w, r, http.StatusForbidden, msgs.Errorf("auth.tokenPorts", t.Perms.Limits.Ports))
			return
		}
	}
	pool, ok := s.portPools(ctx)[host]
	if !ok || pool.From == 0 {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.portPoolOff", host))
		return
	}
	busy := map[int]bool{}
	for _, a := range allocs {
		if a.Host == host {
			busy[a.Port] = true
		}
	}
	port := 0
	for p := pool.From; p <= pool.To; p++ {
		if !busy[p] {
			port = p
			break
		}
	}
	if port == 0 {
		writeErr(w, r, http.StatusConflict, msgs.Errorf("hub.portPoolFull", host))
		return
	}
	alloc := PortAlloc{Host: host, Port: port, Proto: req.Proto, VM: vm.Name, VMPort: req.VMPort, Token: tokenName}
	allocs = append(allocs, alloc)
	if err := s.applyVMPorts(ctx, vm, host, allocs); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if err := s.savePortAllocs(ctx, allocs); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, auth.Username(ctx), "vm.port_open", vm.Name, "ok", alloc)
	writeJSON(w, http.StatusOK, map[string]any{"port": alloc, "address": s.vmPublicAddr(ctx, vm)})
}

// handleVMPortClose — DELETE /hub/hosts/{id}/vm/ports/{port}.
func (s *Server) handleVMPortClose(w http.ResponseWriter, r *http.Request) {
	vm, ok := s.vmMachine(w, r)
	if !ok {
		return
	}
	port, _ := strconv.Atoi(chi.URLParam(r, "port"))
	ctx := r.Context()
	host, err := s.parentName(ctx, vm)
	if err != nil {
		fail(w, r, err)
		return
	}
	portsMu.Lock()
	defer portsMu.Unlock()
	allocs := s.portAllocs(ctx)
	kept := slices.DeleteFunc(slices.Clone(allocs), func(a PortAlloc) bool { return a.Host == host && a.VM == vm.Name && a.Port == port })
	if len(kept) == len(allocs) {
		writeError(w, http.StatusNotFound, msgs.Tc(ctx, "hub.portNotOpen", port))
		return
	}
	if err := s.applyVMPorts(ctx, vm, host, kept); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if err := s.savePortAllocs(ctx, kept); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, auth.Username(ctx), "vm.port_close", vm.Name, "ok", port)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// applyVMPorts — правила хоста для портов машины по списку: есть порты —
// адрес закрепляется и проброс ставится заново; нет — проброс снимается.
func (s *Server) applyVMPorts(ctx context.Context, vm store.Host, host string, allocs []PortAlloc) error {
	user := auth.Username(ctx)
	pfName := "vm-" + vm.Name
	var rules []control.PortRule
	for _, a := range allocs {
		if a.Host == host && a.VM == vm.Name {
			rules = append(rules, control.PortRule{HostPort: a.Port, VMPort: a.VMPort, Proto: a.Proto})
		}
	}
	if len(rules) == 0 {
		_, err := s.hostCall(ctx, user, vm.ParentID, "DELETE", "/api/vm/portforward/"+url.PathEscape(pfName), nil, nil)
		return err
	}
	var fixed struct {
		Address string `json:"address"`
	}
	if _, err := s.hostCall(ctx, user, vm.ParentID, "POST", vmPath(vm, "/fixed-address"), nil, &fixed); err != nil {
		return err
	}
	_, err := s.hostCall(ctx, user, vm.ParentID, "POST", "/api/vm/portforward",
		control.PortForward{Name: pfName, IP: fixed.Address, Rules: rules}, nil)
	return err
}

// releaseVMPorts снимает порты машины (перед её удалением): иначе тот же
// порт, выданный потом другой машине, вёл бы по старому правилу.
func (s *Server) releaseVMPorts(ctx context.Context, vm store.Host) {
	host, err := s.parentName(ctx, vm)
	if err != nil {
		return
	}
	portsMu.Lock()
	defer portsMu.Unlock()
	allocs := s.portAllocs(ctx)
	kept := slices.DeleteFunc(slices.Clone(allocs), func(a PortAlloc) bool { return a.Host == host && a.VM == vm.Name })
	if len(kept) == len(allocs) {
		return
	}
	_ = s.applyVMPorts(ctx, vm, host, kept)
	_ = s.savePortAllocs(ctx, kept)
}
