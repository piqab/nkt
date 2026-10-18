package fly

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Runtime — работающий шлюз: маршрутизатор портов с маршрутами по имени,
// проверки целей всех маршрутов (по их итогам шлюз перестраивает правила
// ядра) и счётчики для карты.
type Runtime struct {
	// Dial — соединение с целью (в тестах — подменяется).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// Listen — приём на порту (в тестах — подменяется).
	Listen func(port int) (net.Listener, error)
	// OnHealthChange — живость целей изменилась (перестроить nftables).
	OnHealthChange func()
	// FromAddr — адрес шлюза в туннеле: источник для PROXY-заголовка проверок.
	FromAddr string
	// GetCertificate — сертификат для маршрутов с TLS на шлюзе (Let's
	// Encrypt у службы; в тестах — свой). nil — такие маршруты не
	// принимаются.
	GetCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// ACME — обработчик проверок Let's Encrypt на порту 80 поверх fallback
	// (перенаправления на https). nil — только перенаправление.
	ACME func(fallback http.Handler) http.Handler

	mu        sync.RWMutex
	cfg       GatewayConfig
	listeners map[int]net.Listener
	health    map[string]*targetState // routeID/targetID
	routes    map[string]*routeCounters
	rr        map[string]*atomic.Uint64
	stopCheck context.CancelFunc
	closed    bool
}

type targetState struct {
	mu        sync.Mutex
	alive     bool
	checked   bool
	fails     int
	oks       int
	lastError string
	latency   time.Duration
	active    atomic.Int64
	total     atomic.Int64
}

type routeCounters struct {
	active, total, failed atomic.Int64
	// denied — отсечено белым списком адресов и ограничениями на клиента.
	denied            atomic.Int64
	bytesIn, bytesOut atomic.Int64
	mu                sync.Mutex
	clients           map[string]int
	// buckets — частота новых соединений по адресам клиентов (Rate).
	buckets map[string]*rateBucket
}

// Пороги проверок: цель выключается после fall неудач подряд, включается
// после rise удач.
const (
	checkFall      = 3
	checkRise      = 2
	checkTimeout   = 3 * time.Second
	dialTimeout    = 5 * time.Second
	peekTimeout    = 10 * time.Second
	defaultCheckIv = 5 * time.Second
	maxClientsShow = 200
)

// NewRuntime — шлюз без маршрутов.
func NewRuntime() *Runtime {
	d := &net.Dialer{Timeout: dialTimeout}
	return &Runtime{
		Dial:      d.DialContext,
		Listen:    func(port int) (net.Listener, error) { return net.Listen("tcp", ":"+strconv.Itoa(port)) },
		listeners: map[int]net.Listener{},
		health:    map[string]*targetState{},
		routes:    map[string]*routeCounters{},
		rr:        map[string]*atomic.Uint64{},
	}
}

func key(route, target string) string { return route + "/" + target }

// Apply — новая конфигурация: порты маршрутов по имени открываются и
// закрываются, счётчики и состояние целей, которые остались, сохраняются.
func (rt *Runtime) Apply(cfg GatewayConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return errors.New("fly: runtime closed")
	}
	want := map[int]bool{}
	health := map[string]*targetState{}
	routes := map[string]*routeCounters{}
	for _, r := range cfg.Routes {
		if r.Named() {
			want[r.Port] = true
		}
		if r.TLS {
			// Проверка Let's Encrypt и перенаправление на https.
			want[80] = true
		}
		rc := rt.routes[r.ID]
		if rc == nil {
			rc = &routeCounters{clients: map[string]int{}}
		}
		routes[r.ID] = rc
		if rt.rr[r.ID] == nil {
			rt.rr[r.ID] = &atomic.Uint64{}
		}
		for _, t := range r.Targets {
			k := key(r.ID, t.ID)
			ts := rt.health[k]
			if ts == nil {
				// До первой проверки — живая: иначе шлюз после
				// перезапуска несколько секунд отказывал бы всем.
				ts = &targetState{alive: true}
			}
			health[k] = ts
		}
	}
	for port, ln := range rt.listeners {
		if !want[port] {
			_ = ln.Close()
			delete(rt.listeners, port)
		}
	}
	var errs []string
	for port := range want {
		if rt.listeners[port] != nil {
			continue
		}
		ln, err := rt.Listen(port)
		if err != nil {
			errs = append(errs, fmt.Sprintf("port %d: %v", port, err))
			continue
		}
		rt.listeners[port] = ln
		go rt.serve(ln, port)
	}
	rt.cfg, rt.health, rt.routes = cfg, health, routes
	if rt.stopCheck != nil {
		rt.stopCheck()
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt.stopCheck = cancel
	for _, r := range cfg.Routes {
		for _, t := range r.Targets {
			go rt.checkLoop(ctx, r, t, health[key(r.ID, t.ID)])
		}
	}
	if len(errs) > 0 {
		return errors.New("fly: " + strings.Join(errs, "; "))
	}
	return nil
}

// Close — закрыть порты и остановить проверки.
func (rt *Runtime) Close() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.closed = true
	for _, ln := range rt.listeners {
		_ = ln.Close()
	}
	if rt.stopCheck != nil {
		rt.stopCheck()
	}
}

// Config — текущая конфигурация.
func (rt *Runtime) Config() GatewayConfig {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cfg
}

// Alive — цель жива по проверкам.
func (rt *Runtime) Alive(routeID, targetID string) bool {
	rt.mu.RLock()
	ts := rt.health[key(routeID, targetID)]
	rt.mu.RUnlock()
	if ts == nil {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.alive
}

func (rt *Runtime) serve(ln net.Listener, port int) {
	for {
		c, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return
		}
		go rt.handle(c, port)
	}
}

// route — маршрут по порту и имени: точное имя важнее шаблона, длинный
// шаблон важнее короткого.
func (rt *Runtime) route(port int, name string) (Route, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	best, bestLen, found := Route{}, -1, false
	for _, r := range rt.cfg.Routes {
		if !r.Named() || r.Port != port {
			continue
		}
		for _, p := range r.Names {
			if !matchName(p, name) {
				continue
			}
			l := len(p)
			if !strings.HasPrefix(p, "*.") {
				l += 1 << 16
			}
			if l > bestLen {
				best, bestLen, found = r, l, true
			}
		}
	}
	return best, found
}

func portKind(cfg GatewayConfig, port int) string {
	for _, r := range cfg.Routes {
		if r.Named() && r.Port == port {
			return r.Kind
		}
	}
	return ""
}

func (rt *Runtime) handle(c net.Conn, port int) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(peekTimeout))
	br := bufio.NewReaderSize(c, maxPeek)
	var name string
	var err error
	if portKind(rt.Config(), port) == KindHTTPS {
		name, _, err = peekSNI(br)
	} else {
		name, _, err = peekHost(br)
	}
	_ = c.SetReadDeadline(time.Time{})
	if err != nil || name == "" {
		return
	}
	r, ok := rt.route(port, name)
	// Порт 80 имени с сертификатом на шлюзе: проверка Let's Encrypt —
	// всегда шлюзу, остальное — шлюзу же (перенаправление на https),
	// если своего маршрута HTTP у имени нет.
	if port == 80 && portKind(rt.Config(), port) != KindHTTPS && rt.tlsName(name) && (!ok || acmeRequest(br)) {
		rt.serveRedirect(c, br)
		return
	}
	if !ok {
		if r.Kind != KindHTTPS && portKind(rt.Config(), port) == KindHTTP {
			_, _ = io.WriteString(c, "HTTP/1.1 421 Misdirected Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		}
		return
	}
	client, _ := netip.ParseAddrPort(c.RemoteAddr().String())
	rt.mu.RLock()
	rc := rt.routes[r.ID]
	rt.mu.RUnlock()
	if rc == nil {
		return
	}
	// Белый список и ограничения на клиента: у маршрута с сертификатом
	// на шлюзе — после рукопожатия (проверка Let's Encrypt по 443 должна
	// пройти).
	if !r.TLS && !rc.admit(r, client.Addr()) {
		return
	}
	rc.total.Add(1)
	order := rt.pick(r, client.Addr())
	if r.TLS {
		rt.serveTLS(c, br, r, rc, client, order)
		return
	}
	var up net.Conn
	var used *targetState
	for i, t := range order {
		if i == 3 {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		up, err = rt.Dial(ctx, "tcp", net.JoinHostPort(t.Host, strconv.Itoa(t.Port)))
		cancel()
		if err == nil {
			rt.mu.RLock()
			used = rt.health[key(r.ID, t.ID)]
			rt.mu.RUnlock()
			break
		}
	}
	if up == nil || used == nil {
		rc.failed.Add(1)
		if up != nil {
			_ = up.Close()
		}
		return
	}
	defer up.Close()
	if r.ProxyProtocol {
		local, _ := netip.ParseAddrPort(c.LocalAddr().String())
		if _, err := up.Write(proxyHeaderV2(client, local)); err != nil {
			rc.failed.Add(1)
			return
		}
	}
	ip := client.Addr().Unmap().String()
	rc.active.Add(1)
	used.active.Add(1)
	used.total.Add(1)
	rc.mu.Lock()
	rc.clients[ip]++
	rc.mu.Unlock()
	defer func() {
		rc.active.Add(-1)
		used.active.Add(-1)
		rc.mu.Lock()
		if rc.clients[ip]--; rc.clients[ip] <= 0 {
			delete(rc.clients, ip)
		}
		rc.mu.Unlock()
	}()
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(up, br)
		rc.bytesIn.Add(n)
		if cw, ok := up.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(c, up)
		rc.bytesOut.Add(n)
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

// pick — цели маршрута в порядке предпочтения: первая — по алгоритму,
// дальше — остальные живые (на случай, если первая не ответит).
func (rt *Runtime) pick(r Route, client netip.Addr) []Target {
	var live []Target
	for _, t := range r.Targets {
		if !t.Drain && rt.Alive(r.ID, t.ID) {
			live = append(live, t)
		}
	}
	if len(live) == 0 {
		return nil
	}
	weight := func(t Target) int {
		if t.Weight <= 0 {
			return 1
		}
		return t.Weight
	}
	var first int
	switch r.Algorithm {
	case AlgoLeastConn:
		rt.mu.RLock()
		best := -1.0
		for i, t := range live {
			load := float64(rt.health[key(r.ID, t.ID)].active.Load()) / float64(weight(t))
			if best < 0 || load < best {
				best, first = load, i
			}
		}
		rt.mu.RUnlock()
	default:
		var slots []int
		for i, t := range live {
			for k := 0; k < weight(t); k++ {
				slots = append(slots, i)
			}
		}
		var n uint64
		if r.Algorithm == AlgoSourceHash {
			h := fnv.New64a()
			_, _ = h.Write(client.Unmap().AsSlice())
			n = h.Sum64()
		} else {
			rt.mu.RLock()
			n = rt.rr[r.ID].Add(1) - 1
			rt.mu.RUnlock()
		}
		first = slots[n%uint64(len(slots))]
	}
	out := []Target{live[first]}
	for i, t := range live {
		if i != first {
			out = append(out, t)
		}
	}
	return out
}

// checkLoop — проверки одной цели, пока конфигурация та же.
func (rt *Runtime) checkLoop(ctx context.Context, r Route, t Target, ts *targetState) {
	iv := time.Duration(r.Check.IntervalS) * time.Second
	if iv <= 0 {
		iv = defaultCheckIv
	}
	tick := time.NewTicker(iv)
	defer tick.Stop()
	for {
		if r.Check.Kind != CheckNone {
			start := time.Now()
			err := rt.checkOnce(ctx, r, t)
			if ctx.Err() != nil {
				return
			}
			changed := ts.record(err, time.Since(start))
			if changed && rt.OnHealthChange != nil {
				rt.OnHealthChange()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// record — итог проверки; true — живость сменилась.
func (ts *targetState) record(err error, took time.Duration) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.checked = true
	was := ts.alive
	if err != nil {
		ts.fails++
		ts.oks = 0
		ts.lastError = err.Error()
		if ts.fails >= checkFall {
			ts.alive = false
		}
	} else {
		ts.oks++
		ts.fails = 0
		ts.lastError = ""
		ts.latency = took
		if ts.oks >= checkRise {
			ts.alive = true
		}
	}
	return was != ts.alive
}

func (rt *Runtime) checkOnce(ctx context.Context, r Route, t Target) error {
	port := t.Port
	if r.Check.Port != 0 {
		port = r.Check.Port
	}
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	c, err := rt.Dial(cctx, "tcp", net.JoinHostPort(t.Host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer c.Close()
	if r.Check.Kind != CheckHTTP {
		return nil
	}
	_ = c.SetDeadline(time.Now().Add(checkTimeout))
	if r.ProxyProtocol {
		src, _ := netip.ParseAddrPort(net.JoinHostPort(orDefault(rt.FromAddr, "10.77.0.1"), "1"))
		dst, _ := netip.ParseAddrPort(net.JoinHostPort(t.Host, strconv.Itoa(port)))
		if _, err := c.Write(proxyHeaderV2(src, dst)); err != nil {
			return err
		}
	}
	host := t.Host
	for _, n := range r.Names {
		if !strings.HasPrefix(n, "*.") {
			host = n
			break
		}
	}
	// HTTPS-маршрут: цель говорит по TLS — проверка HTTP поверх него
	// невозможна без расшифровки; хватает установленного соединения.
	if r.Kind == KindHTTPS {
		return nil
	}
	if _, err := fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: nkt-fly-check\r\nConnection: close\r\n\r\n", r.Check.Path, host); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	f := strings.Fields(line)
	if len(f) < 2 {
		return fmt.Errorf("bad status line %q", strings.TrimSpace(line))
	}
	code, _ := strconv.Atoi(f[1])
	if r.Check.Expect != 0 && code != r.Check.Expect || r.Check.Expect == 0 && (code < 200 || code >= 400) {
		return fmt.Errorf("HTTP %d", code)
	}
	return nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// TargetStats — цель в сводке.
type TargetStats struct {
	ID        string `json:"id"`
	Alive     bool   `json:"alive"`
	Checked   bool   `json:"checked"`
	Drain     bool   `json:"drain,omitempty"`
	Active    int64  `json:"active"`
	Total     int64  `json:"total"`
	LastError string `json:"last_error,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

// ClientCount — клиент маршрута и число его соединений.
type ClientCount struct {
	IP    string `json:"ip"`
	Conns int    `json:"conns"`
}

// RouteStats — маршрут в сводке.
type RouteStats struct {
	ID     string `json:"id"`
	Active int64  `json:"active"`
	Total  int64  `json:"total"`
	Failed int64  `json:"failed"`
	// Denied — отсечено белым списком (маршруты по имени).
	Denied   int64         `json:"denied,omitempty"`
	BytesIn  int64         `json:"bytes_in"`
	BytesOut int64         `json:"bytes_out"`
	Clients  int           `json:"clients"`
	Top      []ClientCount `json:"top,omitempty"`
	Targets  []TargetStats `json:"targets"`
}

// Stats — сводка шлюза.
type Stats struct {
	Revision int64        `json:"revision"`
	Routes   []RouteStats `json:"routes"`
}

// Snapshot — сводка: маршруты по имени — свои счётчики; по порту —
// заполняет шлюз из conntrack и счётчиков nftables (Kernel).
func (rt *Runtime) Snapshot() Stats {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	st := Stats{Revision: rt.cfg.Revision}
	for _, r := range rt.cfg.Routes {
		rs := RouteStats{ID: r.ID}
		if rc := rt.routes[r.ID]; rc != nil {
			rs.Active, rs.Total, rs.Failed, rs.Denied = rc.active.Load(), rc.total.Load(), rc.failed.Load(), rc.denied.Load()
			rs.BytesIn, rs.BytesOut = rc.bytesIn.Load(), rc.bytesOut.Load()
			rc.mu.Lock()
			rs.Clients = len(rc.clients)
			rs.Top = topClients(rc.clients)
			rc.mu.Unlock()
		}
		for _, t := range r.Targets {
			ts := rt.health[key(r.ID, t.ID)]
			if ts == nil {
				continue
			}
			ts.mu.Lock()
			rs.Targets = append(rs.Targets, TargetStats{ID: t.ID, Alive: ts.alive, Checked: ts.checked, Drain: t.Drain,
				Active: ts.active.Load(), Total: ts.total.Load(), LastError: ts.lastError, LatencyMS: ts.latency.Milliseconds()})
			ts.mu.Unlock()
		}
		st.Routes = append(st.Routes, rs)
	}
	return st
}

func topClients(m map[string]int) []ClientCount {
	out := make([]ClientCount, 0, len(m))
	for ip, n := range m {
		out = append(out, ClientCount{IP: ip, Conns: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Conns != out[j].Conns {
			return out[i].Conns > out[j].Conns
		}
		return out[i].IP < out[j].IP
	})
	if len(out) > maxClientsShow {
		out = out[:maxClientsShow]
	}
	return out
}

// rateBucket — ведро токенов частоты новых соединений одного клиента.
type rateBucket struct {
	tokens float64
	last   time.Time
}

// maxBuckets — сколько адресов помнить для частоты; больше — старые
// (без новых соединений больше минуты) забываются.
const maxBuckets = 20000

// admit — клиент допущен к маршруту: белый список, не больше MaxConns
// одновременных соединений и Rate новых в секунду с одного адреса.
// Отказ считается в denied.
func (rc *routeCounters) admit(r Route, a netip.Addr) bool {
	if !r.Allowed(a) {
		rc.denied.Add(1)
		return false
	}
	if r.MaxConns == 0 && r.Rate == 0 {
		return true
	}
	ip := a.Unmap().String()
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if r.MaxConns > 0 && rc.clients[ip] >= r.MaxConns {
		rc.denied.Add(1)
		return false
	}
	if r.Rate > 0 {
		now := time.Now()
		if rc.buckets == nil {
			rc.buckets = map[string]*rateBucket{}
		}
		b := rc.buckets[ip]
		if b == nil {
			if len(rc.buckets) >= maxBuckets {
				for k, v := range rc.buckets {
					if now.Sub(v.last) > time.Minute {
						delete(rc.buckets, k)
					}
				}
			}
			b = &rateBucket{tokens: float64(r.Rate), last: now}
			rc.buckets[ip] = b
		}
		b.tokens += now.Sub(b.last).Seconds() * float64(r.Rate)
		if b.tokens > float64(r.Rate) {
			b.tokens = float64(r.Rate)
		}
		b.last = now
		if b.tokens < 1 {
			rc.denied.Add(1)
			return false
		}
		b.tokens--
	}
	return true
}
