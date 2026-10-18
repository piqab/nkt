package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
)

// Traffic Fly на хосте: туннели WireGuard к шлюзам (по интерфейсу на
// шлюз), DNAT служебных портов на цели и возврат ответов в туннель.
// Закрытые ключи — в каталоге данных nkt (0600), наружу — только
// публичные. Файлы — /etc/wireguard/<iface>.conf (wg-quick@ включён) и
// правила рядом с ключом: после перезагрузки туннели поднимаются сами,
// без nkt и без хаба.

func (s *Server) flyDir() string { return filepath.Join(s.cfg.DataDir, "fly") }

// handleFlyKey — POST /fly/key?iface=nkt-flyN: ключ туннеля (создаётся,
// если нет); ответ — публичный.
func (s *Server) handleFlyKey(w http.ResponseWriter, r *http.Request) {
	iface := r.URL.Query().Get("iface")
	if !fly.ValidIface(iface) {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "fly.badIface"))
		return
	}
	key, err := fly.FilePath(s.flyDir(), iface, ".key")
	if err != nil {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "fly.badIface"))
		return
	}
	pub, err := fly.LoadOrCreateKey(key)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, fly.KeyResponse{PublicKey: pub})
}

// handleFlyConfig — PUT /fly/config: туннели хоста по конфигурации хаба;
// туннели Traffic Fly, которых в ней нет, снимаются.
func (s *Server) handleFlyConfig(w http.ResponseWriter, r *http.Request) {
	if s.cfg.IsFixtures() {
		writeError(w, http.StatusForbidden, msgs.Tc(r.Context(), "pkgInstall.fixturesDisabled"))
		return
	}
	var cfg fly.HostConfig
	if err := decodeJSON(r, &cfg); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := cfg.Validate(); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	// apt-get install wireguard-tools nftables, если их нет, — дольше
	// обычных сроков запроса.
	ctx, cancel := longCall(w, r)
	defer cancel()
	log, err := s.flyApply(ctx, cfg)
	names := make([]string, 0, len(cfg.Ifaces))
	for _, i := range cfg.Ifaces {
		names = append(names, i.Name)
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "fly.apply", strings.Join(names, ","), auditResult(err), errText(err))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": msgs.Localize(msgs.LangFromRequest(r), err), "log": log})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revision": cfg.Revision, "log": log})
}

// flyHostRefresh — интерфейс уже есть: обновить без разрыва; нет —
// включить wg-quick@. $1 iface, $2 адрес, $3 nft-файл, $4 метка, $5
// таблица, $6 адрес шлюза в туннеле, $7 файл закрытого ключа. Ключа в
// конфигурации нет (его ставит PostUp), а syncconf приводит интерфейс
// точно к файлу и ключ стирает — поэтому сразу за ним wg set private-key.
const flyHostRefresh = `set -e
if ip link show "$1" >/dev/null 2>&1; then
  wg syncconf "$1" <(wg-quick strip "$1")
  wg set "$1" private-key "$7"
  ip address replace "$2" dev "$1"
  nft -f "$3"
  ip rule del fwmark "$4" table "$5" 2>/dev/null || true
  ip rule add fwmark "$4" table "$5" priority "$5"
  ip route replace default via "$6" dev "$1" onlink table "$5"
  echo "updated"
else
  systemctl enable --now "wg-quick@$1"
  echo "started"
fi`

// flyHostRemove — снять туннель. $1 iface.
const flyHostRemove = `systemctl disable --now "wg-quick@$1" 2>/dev/null || true
rm -f "/etc/wireguard/$1.conf"
echo "removed"`

func (s *Server) flyApply(ctx context.Context, cfg fly.HostConfig) ([]string, error) {
	var log []string
	if err := os.MkdirAll(s.flyDir(), 0o700); err != nil {
		return log, err
	}
	// Итог — по самим командам, а не по коду apt: apt падает и из-за
	// чужого недонастроенного пакета (grub-pc), когда наши уже стоят.
	res, err := RunUnrestricted(ctx, "sh", "-c", "have() { command -v wg >/dev/null && command -v wg-quick >/dev/null && command -v nft >/dev/null; }; have || { DEBIAN_FRONTEND=noninteractive apt-get install -y wireguard-tools nftables; have; }")
	if err != nil || !res.OK() {
		return log, fmt.Errorf("wireguard-tools/nftables: %s", strings.TrimSpace(res.Output()+" "+errText(err)))
	}
	want := map[string]bool{}
	for _, i := range cfg.Ifaces {
		want[i.Name] = true
		key, err := fly.FilePath(s.flyDir(), i.Name, ".key")
		if err != nil {
			return log, err
		}
		if _, err := fly.LoadOrCreateKey(key); err != nil {
			return log, err
		}
		nftFile, err := fly.FilePath(s.flyDir(), i.Name, ".nft")
		if err != nil {
			return log, err
		}
		if err := os.WriteFile(nftFile, []byte(fly.HostNFT(i)), 0o600); err != nil {
			return log, err
		}
		conf := fly.HostWG(i, key, nftFile)
		res, err := RunUnrestrictedInput(ctx, []byte(conf), "sh", "-c", `umask 077; mkdir -p /etc/wireguard && cat > "$1.tmp" && mv "$1.tmp" "$1"`, "sh", "/etc/wireguard/"+i.Name+".conf")
		if err != nil || !res.OK() {
			return log, fmt.Errorf("%s: wireguard config: %s", i.Name, strings.TrimSpace(res.Output()+" "+errText(err)))
		}
		gw := netip.MustParseAddr(i.Gateway.Address).String()
		res, err = RunUnrestricted(ctx, "bash", "-c", flyHostRefresh, "fly", i.Name, i.Address, nftFile,
			"0x"+strconv.FormatUint(uint64(i.Mark), 16), strconv.Itoa(i.Table), gw, key)
		if err != nil || !res.OK() {
			return log, fmt.Errorf("%s: %s", i.Name, strings.TrimSpace(res.Output()+" "+errText(err)))
		}
		log = append(log, i.Name+": "+strings.TrimSpace(res.Stdout))
	}
	// Снять туннели, которых больше нет (по прежней конфигурации хоста).
	prev := s.flyHostConfig()
	for _, i := range prev.Ifaces {
		if want[i.Name] || !fly.ValidIface(i.Name) {
			continue
		}
		res, err := RunUnrestricted(ctx, "sh", "-c", flyHostRemove, "fly", i.Name)
		if err != nil || !res.OK() {
			log = append(log, i.Name+": "+strings.TrimSpace(res.Output()+" "+errText(err)))
			continue
		}
		for _, ext := range []string{".nft", ".key"} {
			if p, err := fly.FilePath(s.flyDir(), i.Name, ext); err == nil {
				_ = os.Remove(p)
			}
		}
		log = append(log, i.Name+": removed")
	}
	raw, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(s.flyDir(), "host.json"), raw, 0o600); err != nil {
		return log, err
	}
	return log, nil
}

// flyHostConfig — что хост поднял по последней конфигурации.
func (s *Server) flyHostConfig() fly.HostConfig {
	var cfg fly.HostConfig
	if raw, err := os.ReadFile(filepath.Join(s.flyDir(), "host.json")); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	return cfg
}

// handleFlyStatus — GET /fly/status: туннели хоста (рукопожатие с шлюзом).
func (s *Server) handleFlyStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.flyHostConfig()
	st := fly.HostStatus{Revision: cfg.Revision, Now: time.Now().Unix(), Ifaces: []fly.HostIfaceStatus{}}
	for _, i := range cfg.Ifaces {
		is := fly.HostIfaceStatus{Name: i.Name}
		s.flyHealKey(r.Context(), i.Name)
		res, err := RunUnrestricted(r.Context(), "wg", "show", i.Name, "dump")
		switch {
		case err != nil:
			is.Error = err.Error()
		case !res.OK():
			is.Error = strings.TrimSpace(res.Output())
		default:
			if peers := fly.ParseWGDump(res.Stdout); len(peers) > 0 {
				p := peers[0]
				is.Handshake, is.RxBytes, is.TxBytes = p.Handshake, p.RxBytes, p.TxBytes
				is.Up = fly.TunnelUp(p.Handshake, st.Now)
			}
		}
		st.Ifaces = append(st.Ifaces, is)
	}
	writeJSON(w, http.StatusOK, st)
}

// flyHealKey — ключ на интерфейсе туннеля не тот или пропал (стёр
// syncconf прежних версий) — поставить из файла.
func (s *Server) flyHealKey(ctx context.Context, iface string) {
	key, err := fly.FilePath(s.flyDir(), iface, ".key")
	if err != nil {
		return
	}
	pub, err := fly.LoadOrCreateKey(key)
	if err != nil {
		return
	}
	res, err := RunUnrestricted(ctx, "wg", "show", iface, "public-key")
	if err != nil || !res.OK() || strings.TrimSpace(res.Stdout) == pub {
		return
	}
	_, _ = RunUnrestricted(ctx, "wg", "set", iface, "private-key", key)
}

// flyWatchEvery — как часто хост сам сверяет туннели Traffic Fly: адреса
// целей и ключ интерфейса. Работает и без хаба.
const flyWatchEvery = 30 * time.Second

func (s *Server) flyWatchLoop(ctx context.Context) {
	if s.cfg == nil || s.cfg.IsFixtures() {
		return
	}
	t := time.NewTicker(flyWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.flyWatchOnce(ctx)
	}
}

// flyWatchOnce — ключ на каждом интерфейсе на месте; цель-контейнер,
// машина или LXD сменила адрес (пересоздана) — правило DNAT переписать.
func (s *Server) flyWatchOnce(ctx context.Context) {
	cfg := s.flyHostConfig()
	if len(cfg.Ifaces) == 0 {
		return
	}
	for _, i := range cfg.Ifaces {
		if fly.ValidIface(i.Name) {
			s.flyHealKey(ctx, i.Name)
		}
	}
	if s.scanner == nil {
		return
	}
	snap, err := s.scanner.LatestOrScan(ctx)
	if err != nil || snap == nil {
		return
	}
	s.flyFollowTargets(ctx, cfg, snap)
}

// flyFollowTargets — цели-контейнеры, машины и LXD с новым адресом
// (пересозданы) — правило DNAT переписать.
func (s *Server) flyFollowTargets(ctx context.Context, cfg fly.HostConfig, snap *model.Snapshot) {
	changed := false
	for ii := range cfg.Ifaces {
		i := &cfg.Ifaces[ii]
		ifaceChanged := false
		for si := range i.Services {
			sv := &i.Services[si]
			ip := flyTargetIP(snap, sv.Kind, sv.Ref)
			if ip == "" || ip == sv.Addr {
				continue
			}
			if s.log != nil {
				s.log.Info("fly: target address changed", "iface", i.Name, "kind", sv.Kind, "ref", sv.Ref, "from", sv.Addr, "to", ip)
			}
			sv.Addr, ifaceChanged = ip, true
		}
		if !ifaceChanged || !fly.ValidIface(i.Name) {
			continue
		}
		nftFile, err := fly.FilePath(s.flyDir(), i.Name, ".nft")
		if err != nil {
			continue
		}
		if err := os.WriteFile(nftFile, []byte(fly.HostNFT(*i)), 0o600); err != nil {
			continue
		}
		if res, err := RunUnrestricted(ctx, "nft", "-f", nftFile); err == nil && res.OK() {
			changed = true
		}
	}
	if changed && cfg.Validate() == nil {
		raw, _ := json.MarshalIndent(cfg, "", "  ")
		_ = os.WriteFile(filepath.Join(s.flyDir(), "host.json"), raw, 0o600)
	}
}

// flyTargetIP — текущий IPv4 цели по инвентарю хоста; пусто — не
// найдена или вид не про адрес (host, addr).
func flyTargetIP(snap *model.Snapshot, kind, ref string) string {
	v4 := func(s string) string {
		if a, err := netip.ParseAddr(s); err == nil && a.Is4() {
			return a.String()
		}
		return ""
	}
	switch kind {
	case "container":
		for _, c := range snap.Container {
			if c.Name == ref {
				for _, n := range c.Networks {
					if ip := v4(n.IPAddress); ip != "" {
						return ip
					}
				}
			}
		}
	case "vm":
		for _, v := range snap.VMs {
			if v.Name == ref {
				for _, n := range v.Networks {
					if ip := v4(n.IP); ip != "" {
						return ip
					}
				}
			}
		}
	case "lxd":
		for _, in := range snap.LXD {
			if in.Name == ref {
				for _, a := range in.IPv4 {
					if ip := v4(a); ip != "" {
						return ip
					}
				}
			}
		}
	}
	return ""
}

// handleFlyRefresh — POST /fly/refresh: свежий скан хоста; адреса целей
// Traffic Fly сверяются с ним сейчас, а не через 30 секунд; ответ —
// контейнеры с проектом и сервисом compose (выкладка конвейера находит по
// ним контейнер сервиса).
func (s *Server) handleFlyRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var snap *model.Snapshot
	var err error
	if s.cfg.IsFixtures() {
		snap, err = s.scanner.LatestOrScan(ctx)
	} else {
		snap, err = s.scanner.Scan(ctx)
	}
	if err != nil || snap == nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	if !s.cfg.IsFixtures() {
		if cfg := s.flyHostConfig(); len(cfg.Ifaces) > 0 {
			s.flyFollowTargets(ctx, cfg, snap)
		}
	}
	type item struct {
		Name    string `json:"name"`
		Project string `json:"project,omitempty"`
		Service string `json:"service,omitempty"`
		State   string `json:"state,omitempty"`
		IP      string `json:"ip,omitempty"`
	}
	out := []item{}
	for _, c := range snap.Container {
		out = append(out, item{Name: c.Name, Project: c.Project, Service: c.ServiceName, State: c.State, IP: flyTargetIP(snap, "container", c.Name)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": out})
}
