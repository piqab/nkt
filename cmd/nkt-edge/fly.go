package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/piqab/nkt/internal/fly"
)

// Traffic Fly на шлюзе: «nkt-edge fly» — отдельная служба nkt-fly с
// правами на сеть (основной nkt-edge их не имеет и иметь не должен).
// Поднимает WireGuard к хостам, правила nftables для маршрутов по порту,
// маршрутизатор 80/443 по именам, проверки целей и сводку. Команды хаба
// приходят от основного nkt-edge по loopback (FLY_API_ADDR) с токеном
// edge. Конфигурация хранится в FLY_DIR/fly.json: после перезагрузки
// шлюз поднимает всё сам, хаб для работы не нужен.

const (
	flyHeader = "X-Fly-Token"
	flyIface  = "nkt-fly"
)

type flyDaemon struct {
	token  string
	dir    string
	wgConf string
	rt     *fly.Runtime

	mu      sync.Mutex
	pub     string
	errs    []string
	nftDirt chan struct{}
}

func runFly() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	token := os.Getenv("EDGE_TOKEN")
	if len(token) < 32 {
		log.Fatal("EDGE_TOKEN must be at least 32 characters")
	}
	d := &flyDaemon{
		token:   token,
		dir:     env("FLY_DIR", "/etc/nkt-edge/fly"),
		wgConf:  "/etc/wireguard/" + flyIface + ".conf",
		rt:      fly.NewRuntime(),
		nftDirt: make(chan struct{}, 1),
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		log.Fatalf("fly dir: %v", err)
	}
	pub, err := fly.LoadOrCreateKey(filepath.Join(d.dir, "wg.key"))
	if err != nil {
		log.Fatalf("wireguard key: %v", err)
	}
	d.pub = pub
	d.rt.OnHealthChange = d.markNFT
	if raw, err := os.ReadFile(filepath.Join(d.dir, "fly.json")); err == nil {
		var cfg fly.GatewayConfig
		if json.Unmarshal(raw, &cfg) == nil {
			if err := d.apply(cfg, false); err != nil {
				log.Printf("fly: saved config: %v", err)
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go d.nftLoop(ctx)

	addr := env("FLY_API_ADDR", "127.0.0.1:8446")
	if host, _, err := net.SplitHostPort(addr); err != nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("FLY_API_ADDR must be a loopback address with a port")
	}
	mux := http.NewServeMux()
	mux.HandleFunc(fly.PathKey, d.auth(d.handleKey))
	mux.HandleFunc(fly.PathConfig, d.auth(d.handleConfig))
	mux.HandleFunc(fly.PathStatus, d.auth(d.handleStatus))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("fly api: %v", err)
		}
	}()
	log.Printf("nkt-fly %s: gateway key %s", version, pub)
	<-ctx.Done()
	sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(sh)
	d.rt.Close()
}

func (d *flyDaemon) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(flyHeader)), []byte(d.token)) != 1 {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func writeFlyJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (d *flyDaemon) handleKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	writeFlyJSON(w, 200, fly.KeyResponse{PublicKey: d.pub})
}

func (d *flyDaemon) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.NotFound(w, r)
		return
	}
	var cfg fly.GatewayConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&cfg); err != nil {
		writeFlyJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if err := d.apply(cfg, true); err != nil {
		writeFlyJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	writeFlyJSON(w, 200, map[string]any{"ok": true, "revision": cfg.Revision})
}

func (d *flyDaemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeFlyJSON(w, 200, d.status(r.Context()))
}

// apply — WireGuard, маршрутизатор, nftables, ufw; save — записать
// fly.json (при подъёме из него же — не надо).
func (d *flyDaemon) apply(cfg fly.GatewayConfig, save bool) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	var errs []string
	if err := writeFileAtomic(d.wgConf, []byte(fly.GatewayWG(cfg, filepath.Join(d.dir, "wg.key"))), 0o600); err != nil {
		return fmt.Errorf("wireguard config: %w", err)
	}
	if err := wgUp(cfg); err != nil {
		errs = append(errs, err.Error())
	}
	if err := d.rt.Apply(cfg); err != nil {
		errs = append(errs, err.Error())
	}
	if err := d.writeNFT(); err != nil {
		errs = append(errs, err.Error())
	}
	if err := ufwPorts(d.dir, cfg); err != nil {
		errs = append(errs, err.Error())
	}
	if save {
		raw, _ := json.MarshalIndent(cfg, "", "  ")
		if err := writeFileAtomic(filepath.Join(d.dir, "fly.json"), raw, 0o600); err != nil {
			errs = append(errs, err.Error())
		}
	}
	d.mu.Lock()
	d.errs = errs
	d.mu.Unlock()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// wgUp — интерфейс поднят: есть — обновить без разрыва (wg syncconf),
// нет — включить wg-quick@nkt-fly.
func wgUp(cfg fly.GatewayConfig) error {
	if exec.Command("ip", "link", "show", flyIface).Run() == nil {
		if out, err := exec.Command("bash", "-c", "wg syncconf "+flyIface+" <(wg-quick strip "+flyIface+")").CombinedOutput(); err != nil {
			return fmt.Errorf("wg syncconf: %v: %s", err, strings.TrimSpace(string(out)))
		}
		_ = exec.Command("ip", "address", "replace", cfg.Address, "dev", flyIface).Run()
		return nil
	}
	if out, err := exec.Command("systemctl", "enable", "--now", "wg-quick@"+flyIface).CombinedOutput(); err != nil {
		return fmt.Errorf("wg-quick: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *flyDaemon) markNFT() {
	select {
	case d.nftDirt <- struct{}{}:
	default:
	}
}

// nftLoop — живость целей изменилась: правила ядра перестраиваются (не
// чаще раза в секунду).
func (d *flyDaemon) nftLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.nftDirt:
			time.Sleep(time.Second)
			if err := d.writeNFT(); err != nil {
				log.Printf("fly: nft: %v", err)
			}
		}
	}
}

func (d *flyDaemon) writeNFT() error {
	cfg := d.rt.Config()
	if cfg.Iface == "" {
		return nil
	}
	p := filepath.Join(d.dir, "gateway.nft")
	if err := writeFileAtomic(p, []byte(fly.GatewayNFT(cfg, d.rt.Alive)), 0o600); err != nil {
		return err
	}
	if out, err := exec.Command("nft", "-f", p).CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ufwPorts — включённый ufw иначе не пустит порт WireGuard и порты
// маршрутизатора (маршруты по порту идут через FORWARD — ему правило в
// wg-quick). Открытые прежде, но ненужные теперь, — закрываются.
func ufwPorts(dir string, cfg fly.GatewayConfig) error {
	out, err := exec.Command("ufw", "status").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Status: active") {
		return nil
	}
	want := map[string]bool{strconv.Itoa(cfg.ListenPort) + "/udp": true}
	for _, r := range cfg.Routes {
		if r.Named() {
			want[strconv.Itoa(r.Port)+"/tcp"] = true
		}
	}
	state := filepath.Join(dir, "ufw.json")
	var had []string
	if raw, err := os.ReadFile(state); err == nil {
		_ = json.Unmarshal(raw, &had)
	}
	var errs []string
	for _, p := range had {
		if !want[p] {
			_ = exec.Command("ufw", "delete", "allow", p).Run()
		}
	}
	var now []string
	for p := range want {
		if o, err := exec.Command("ufw", "allow", p, "comment", "nkt Traffic Fly").CombinedOutput(); err != nil {
			errs = append(errs, fmt.Sprintf("ufw allow %s: %s", p, strings.TrimSpace(string(o))))
			continue
		}
		now = append(now, p)
	}
	sort.Strings(now)
	raw, _ := json.Marshal(now)
	_ = writeFileAtomic(state, raw, 0o600)
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// status — сводка: маршрутизатор и проверки — из Runtime; маршруты ядра
// — соединения из conntrack по метке; туннели — wg show dump.
func (d *flyDaemon) status(ctx context.Context) fly.GatewayStatus {
	cfg := d.rt.Config()
	snap := d.rt.Snapshot()
	st := fly.GatewayStatus{Revision: snap.Revision, PublicKey: d.pub, Now: time.Now().Unix(), Routes: snap.Routes}
	for i, r := range cfg.Routes {
		if r.Named() || i >= len(st.Routes) {
			continue
		}
		out, err := exec.CommandContext(ctx, "conntrack", "-L", "-m", strconv.FormatUint(uint64(fly.RouteMark(i+1)), 10)).Output()
		if err != nil && len(out) == 0 {
			continue
		}
		cs := fly.ParseConntrack(string(out))
		rs := &st.Routes[i]
		rs.Active = int64(cs.Conns)
		rs.BytesIn = cs.Bytes
		rs.Clients = len(cs.Clients)
		rs.Top = topOf(cs.Clients)
	}
	if out, err := exec.CommandContext(ctx, "wg", "show", flyIface, "dump").Output(); err == nil {
		st.Peers = fly.ParseWGDump(string(out))
	}
	d.mu.Lock()
	st.Errors = append(st.Errors, d.errs...)
	d.mu.Unlock()
	return st
}

func topOf(m map[string]int) []fly.ClientCount {
	out := make([]fly.ClientCount, 0, len(m))
	for ip, n := range m {
		out = append(out, fly.ClientCount{IP: ip, Conns: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Conns != out[j].Conns {
			return out[i].Conns > out[j].Conns
		}
		return out[i].IP < out[j].IP
	})
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
