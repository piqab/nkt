package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/ups"
)

// ИБП через NUT: состояние (upsc), поиск ИБП и следов UPSilon, подключение
// фоновым заданием (пакет nut, ups.conf, upsd только на 127.0.0.1, откат
// при неудаче) и пороги оповещений.

// upsThresholdsKey — пороги оповещений ИБП в kv хоста (JSON); история —
// upsThresholdsKey + ".history".
const upsThresholdsKey = "ups.thresholds"

// UPSThresholds — когда ИБП на батарее — проблема посерьёзнее: заряд ниже
// ChargePct или осталось меньше RuntimeMin минут.
type UPSThresholds struct {
	ChargePct  int `json:"charge_pct"`
	RuntimeMin int `json:"runtime_min"`
}

var defaultUPSThresholds = UPSThresholds{ChargePct: 30, RuntimeMin: 5}

func (t UPSThresholds) valid() bool {
	return t.ChargePct >= 5 && t.ChargePct <= 95 && t.RuntimeMin >= 1 && t.RuntimeMin <= 120
}

func (s *Server) upsThresholds(ctx context.Context) UPSThresholds {
	if raw, ok, err := s.db.KVGet(ctx, upsThresholdsKey); err == nil && ok {
		var t UPSThresholds
		if json.Unmarshal([]byte(raw), &t) == nil && t.valid() {
			return t
		}
	}
	return defaultUPSThresholds
}

// upsCache — последний ответ upsc на 10 с: обзор (его опрашивает хаб) и
// находки спрашивают ИБП в одном запросе.
var upsCache struct {
	sync.Mutex
	at   time.Time
	list []model.UPS
}

// upsStatus — ИБП хоста сейчас (на фикстурах — из заготовленного upsc).
func (s *Server) upsStatus(ctx context.Context) []model.UPS {
	if s.scanner == nil {
		return nil
	}
	upsCache.Lock()
	defer upsCache.Unlock()
	if time.Since(upsCache.at) < 10*time.Second {
		return upsCache.list
	}
	upsCache.list = ups.Status(ctx, s.scanner.Collector())
	upsCache.at = time.Now()
	return upsCache.list
}

// upsFindings — находки ИБП по порогам хоста.
func (s *Server) upsFindings(ctx context.Context) []model.Finding {
	list := s.upsStatus(ctx)
	if len(list) == 0 {
		return nil
	}
	t := s.upsThresholds(ctx)
	return ups.Findings(list, t.ChargePct, t.RuntimeMin)
}

// handleUPS — GET /ups: ИБП, пороги, выключение хоста при садящейся
// батарее (upsmon).
func (s *Server) handleUPS(w http.ResponseWriter, r *http.Request) {
	list := s.upsStatus(r.Context())
	if list == nil {
		list = []model.UPS{}
	}
	c := s.scanner.Collector()
	shutdown := false
	if raw, err := c.ReadFile("/etc/nut/upsmon.conf"); err == nil {
		shutdown = upsmonMonitors(string(raw))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ups": list, "thresholds": s.upsThresholds(r.Context()), "shutdown": shutdown})
}

var upsmonMonitorRe = regexp.MustCompile(`(?m)^\s*MONITOR\s+\S+`)

func upsmonMonitors(conf string) bool { return upsmonMonitorRe.MatchString(conf) }

// handleUPSDetect — GET /ups/detect: что нашлось (без изменений).
func (s *Server) handleUPSDetect(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ups.Detect(r.Context(), s.scanner.Collector()))
}

// handleUPSThresholds — PUT /ups/thresholds: новые пороги; прежние — в
// историю (последние 20).
func (s *Server) handleUPSThresholds(w http.ResponseWriter, r *http.Request) {
	var t UPSThresholds
	if err := decodeJSON(r, &t); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if !t.valid() {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "ups.badThresholds"))
		return
	}
	ctx := r.Context()
	prev := s.upsThresholds(ctx)
	var hist []map[string]any
	if raw, ok, _ := s.db.KVGet(ctx, upsThresholdsKey+".history"); ok {
		_ = json.Unmarshal([]byte(raw), &hist)
	}
	hist = append([]map[string]any{{"ts": time.Now().UTC().Format(time.RFC3339), "user": auth.Username(ctx), "charge_pct": prev.ChargePct, "runtime_min": prev.RuntimeMin}}, hist...)
	if len(hist) > 20 {
		hist = hist[:20]
	}
	rawHist, _ := json.Marshal(hist)
	raw, _ := json.Marshal(t)
	err := s.db.KVSet(ctx, upsThresholdsKey, string(raw))
	if err == nil {
		err = s.db.KVSet(ctx, upsThresholdsKey+".history", string(rawHist))
	}
	s.db.Audit(ctx, auth.Username(ctx), "ups.thresholds", fmt.Sprintf("%d%%/%dmin", t.ChargePct, t.RuntimeMin), auditResult(err), nil)
	if err != nil {
		writeErr(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"thresholds": t, "history": hist})
}

// handleUPSThresholdsHistory — GET /ups/thresholds/history.
func (s *Server) handleUPSThresholdsHistory(w http.ResponseWriter, r *http.Request) {
	var hist []map[string]any
	if raw, ok, _ := s.db.KVGet(r.Context(), upsThresholdsKey+".history"); ok {
		_ = json.Unmarshal([]byte(raw), &hist)
	}
	if hist == nil {
		hist = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": hist})
}

var (
	upsDriverRe = regexp.MustCompile(`^(nutdrv_qx|usbhid-ups|blazer_usb|blazer_ser)$`)
	upsPortRe   = regexp.MustCompile(`^(auto|/dev/tty(S|USB|ACM)[0-9]{1,3})$`)
	upsHexRe    = regexp.MustCompile(`^([0-9a-f]{4})?$`)
	upsUnitRe   = regexp.MustCompile(`^(/etc/systemd/system|/lib/systemd/system|/usr/lib/systemd/system|/etc/init\.d)/[A-Za-z0-9_.@-]{1,80}$`)
)

// handleUPSConnectWS — GET/POST /ups/connect/ws?driver=&port=&vendor=&product=&shutdown=1&upsilon=…:
// подключение ИБП (POST ?job=1 — фоновым заданием). Параметры —
// окружением сценария, каждый проверен.
func (s *Server) handleUPSConnectWS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Mode == config.ModeFixtures {
		writeError(w, http.StatusForbidden, msgs.Tc(r.Context(), "pkgInstall.fixturesDisabled"))
		return
	}
	env, ok := upsConnectEnv(r.URL.Query())
	if !ok {
		writeError(w, http.StatusBadRequest, msgs.Tc(r.Context(), "ups.badParams"))
		return
	}
	buildCmd := func() *exec.Cmd {
		return unrestrictedCommand(env, "bash", "-c", upsConnectScript)
	}
	s.runUpdateSession(w, r, "ups-connect", buildCmd, "ups.connect", env["NKT_UPS_DRIVER"]+" "+env["NKT_UPS_PORT"], s.cfg.TerminalIdleTimeout)
}

// upsConnectEnv — окружение сценария подключения по параметрам запроса;
// ok=false — что-то не прошло проверку (в сценарий не попадает ничего
// непроверенного).
func upsConnectEnv(q url.Values) (map[string]string, bool) {
	driver, port := q.Get("driver"), q.Get("port")
	vendor, product := q.Get("vendor"), q.Get("product")
	units := q["upsilon"]
	if port == "" {
		port = "auto"
	}
	ok := upsDriverRe.MatchString(driver) && upsPortRe.MatchString(port) && upsHexRe.MatchString(vendor) && upsHexRe.MatchString(product) && len(units) <= 4
	for _, u := range units {
		ok = ok && upsUnitRe.MatchString(u)
	}
	// USB-драйвер — порт auto, драйвер COM — путь к порту; nutdrv_qx — любой.
	if driver == "blazer_ser" && port == "auto" || driver != "blazer_ser" && driver != "nutdrv_qx" && port != "auto" {
		ok = false
	}
	if !ok {
		return nil, false
	}
	shutdown := "0"
	if q.Get("shutdown") == "1" {
		shutdown = "1"
	}
	return map[string]string{
		"TERM": "xterm-256color", "DEBIAN_FRONTEND": "noninteractive",
		"NKT_UPS_DRIVER": driver, "NKT_UPS_PORT": port, "NKT_UPS_VENDOR": vendor, "NKT_UPS_PRODUCT": product,
		"NKT_UPS_SHUTDOWN": shutdown, "NKT_UPS_UPSILON": strings.Join(units, " "),
	}, true
}

// upsConnectScript — подключение ИБП к NUT. Прежний /etc/nut — в
// резервную копию; UPSilon останавливается и выключается (файлы на
// месте); ИБП не ответил за 40 с — всё возвращается как было.
const upsConnectScript = `set -e
export DEBIAN_FRONTEND=noninteractive
NAME=ups
BACKUP=/var/lib/netknownsthat/nut-backup-$(date +%Y%m%d-%H%M%S)
STOPPED=""
rollback() {
  echo "ИБП не ответил — возвращаю как было / the UPS did not answer — rolling back"
  systemctl disable --now nut-monitor nut-server >/dev/null 2>&1 || true
  upsdrvctl stop >/dev/null 2>&1 || true
  if [ -d "$BACKUP/nut" ]; then rm -rf /etc/nut && cp -a "$BACKUP/nut" /etc/nut; else rm -f /etc/nut/ups.conf; fi
  for u in $STOPPED; do
    case "$u" in
      /etc/init.d/*) "$u" start || true; command -v update-rc.d >/dev/null && update-rc.d "$(basename "$u")" enable || true ;;
      *) systemctl enable --now "$(basename "$u")" || true ;;
    esac
  done
}
trap 'rc=$?; if [ $rc -ne 0 ]; then rollback; fi; exit $rc' EXIT

mkdir -p "$BACKUP"
if [ -d /etc/nut ]; then cp -a /etc/nut "$BACKUP/nut"; echo "Резервная копия /etc/nut / backup: $BACKUP"; fi

for u in $NKT_UPS_UPSILON; do
  echo "Останавливаю UPSilon / stopping UPSilon: $u"
  case "$u" in
    /etc/init.d/*) "$u" stop || true; command -v update-rc.d >/dev/null && update-rc.d "$(basename "$u")" disable || true ;;
    *) systemctl disable --now "$(basename "$u")" || true ;;
  esac
  STOPPED="$STOPPED $u"
done
if pgrep -i upsilon >/dev/null 2>&1; then
  echo "UPSilon ещё работает — останавливаю процесс / UPSilon still running — stopping it"
  pkill -i upsilon || true
  sleep 2
fi

if ! command -v upsc >/dev/null 2>&1 || ! command -v upsdrvctl >/dev/null 2>&1; then
  apt-get update
  apt-get install -y nut
fi
mkdir -p /etc/nut

PASS=$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)
echo 'MODE=standalone' > /etc/nut/nut.conf
{
  echo "# nkt: ИБП, найденный мастером «Подключить ИБП»"
  echo "[$NAME]"
  echo "  driver = $NKT_UPS_DRIVER"
  echo "  port = $NKT_UPS_PORT"
  [ -n "$NKT_UPS_VENDOR" ] && echo "  vendorid = $NKT_UPS_VENDOR"
  [ -n "$NKT_UPS_PRODUCT" ] && echo "  productid = $NKT_UPS_PRODUCT"
  echo '  desc = "nkt"'
} > /etc/nut/ups.conf
printf 'LISTEN 127.0.0.1 3493\nLISTEN ::1 3493\n' > /etc/nut/upsd.conf
printf '[nkt-monitor]\n  password = %s\n  upsmon primary\n' "$PASS" > /etc/nut/upsd.users
{
  echo "MONITOR $NAME@localhost 1 nkt-monitor $PASS primary"
  echo 'SHUTDOWNCMD "/sbin/shutdown -h +0"'
  echo 'MINSUPPLIES 1'
} > /etc/nut/upsmon.conf
chown root:nut /etc/nut/*.conf /etc/nut/upsd.users 2>/dev/null || true
chmod 640 /etc/nut/*.conf /etc/nut/upsd.users

echo "Запускаю драйвер / starting the driver: $NKT_UPS_DRIVER ($NKT_UPS_PORT)"
systemctl daemon-reload || true
upsdrvctl stop >/dev/null 2>&1 || true
systemctl restart nut-driver.target 2>/dev/null || systemctl restart "nut-driver@$NAME" 2>/dev/null || upsdrvctl start
systemctl enable --now nut-server
systemctl restart nut-server
if [ "$NKT_UPS_SHUTDOWN" = "1" ]; then
  systemctl enable --now nut-monitor
  echo "Выключение хоста при садящейся батарее включено / host shutdown on low battery enabled"
else
  systemctl disable --now nut-monitor >/dev/null 2>&1 || true
  sed -i 's/^MONITOR /# MONITOR /' /etc/nut/upsmon.conf
fi

ok=""
for i in $(seq 1 20); do
  if upsc "$NAME@localhost" ups.status >/dev/null 2>&1; then ok=1; break; fi
  sleep 2
done
[ -n "$ok" ] || exit 1
echo "ИБП отвечает / the UPS answers:"
upsc "$NAME@localhost" 2>/dev/null | grep -E '^(device\.(mfr|model)|ups\.status|battery\.charge|ups\.load|input\.voltage):' || true
`

// upsLow — имена ИБП на батарее ниже порогов хоста.
func (s *Server) upsLow(ctx context.Context) []string {
	t := s.upsThresholds(ctx)
	var out []string
	for _, u := range s.upsStatus(ctx) {
		if ups.LowOnBattery(u, t.ChargePct, t.RuntimeMin) {
			out = append(out, u.Name)
		}
	}
	return out
}
