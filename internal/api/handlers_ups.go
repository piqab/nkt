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
	// upsAutostartRe — где UPSilon запускается: служба, init.d, rc.local, cron.
	upsAutostartRe = regexp.MustCompile(`^((/etc/systemd/system|/lib/systemd/system|/usr/lib/systemd/system)/[A-Za-z0-9_.@-]{1,80}\.service|/etc/init\.d/[A-Za-z0-9_.-]{1,80}|/etc/rc\.local|/etc/rc\.d/rc\.local|/etc/crontab|/etc/cron\.d/[A-Za-z0-9_.-]{1,80})$`)
	upsPIDRe       = regexp.MustCompile(`^[1-9][0-9]{1,8}$`)
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
	autostart, pids := q["autostart"], q["pid"]
	if port == "" {
		port = "auto"
	}
	ok := upsDriverRe.MatchString(driver) && upsPortRe.MatchString(port) && upsHexRe.MatchString(vendor) && upsHexRe.MatchString(product) &&
		len(autostart) <= 8 && len(pids) <= 16
	for _, a := range autostart {
		ok = ok && upsAutostartRe.MatchString(a)
	}
	for _, p := range pids {
		ok = ok && upsPIDRe.MatchString(p)
	}
	if !upsDriverPortOK(driver, port) {
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
		"NKT_UPS_DRIVER": driver, "NKT_UPS_PORT": port,
		"NKT_UPS_CANDIDATES": strings.Join(upsCandidates(driver, port, vendor, product), " "),
		"NKT_UPS_SHUTDOWN":   shutdown, "NKT_UPS_AUTOSTART": strings.Join(autostart, " "), "NKT_UPS_PIDS": strings.Join(pids, " "),
	}, true
}

// upsDriverPortOK — драйвер COM — путь к порту, USB-драйвер — auto,
// nutdrv_qx — любой.
func upsDriverPortOK(driver, port string) bool {
	switch driver {
	case "nutdrv_qx":
		return true
	case "blazer_ser":
		return port != "auto"
	default:
		return port == "auto"
	}
}

// upsCandidates — варианты по очереди: выбранный, затем тот же порт
// другим драйвером Megatec, затем nutdrv_qx по USB. Каждый —
// «драйвер|порт|vendorid|productid».
func upsCandidates(driver, port, vendor, product string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(d, p, v, pr string) {
		if !upsDriverPortOK(d, p) {
			return
		}
		if p != "auto" {
			v, pr = "", ""
		}
		k := d + "|" + p + "|" + v + "|" + pr
		if !seen[d+"|"+p] {
			seen[d+"|"+p] = true
			out = append(out, k)
		}
	}
	add(driver, port, vendor, product)
	if port != "auto" {
		add("nutdrv_qx", port, "", "")
		add("blazer_ser", port, "", "")
		add("nutdrv_qx", "auto", vendor, product)
	} else if driver == "nutdrv_qx" {
		add("blazer_usb", "auto", vendor, product)
	} else if driver == "blazer_usb" {
		add("nutdrv_qx", "auto", vendor, product)
	}
	return out
}

// upsConnectScript — подключение ИБП к NUT. Прежний /etc/nut — в
// резервную копию; UPSilon останавливается, его автозапуск выключается
// (строки rc.local и cron комментируются, копии — в резервной копии).
// Варианты драйвера и порта пробуются по очереди (NKT_UPS_CANDIDATES:
// «драйвер|порт|vendorid|productid» через пробел), на каждый — до 40 с;
// не ответил — в журнал ответ драйвера. Ни один не подошёл — всё
// возвращается как было, UPSilon запускается снова.
const upsConnectScript = `# nkt:quiet
set -e
export DEBIAN_FRONTEND=noninteractive
NAME=ups
BACKUP=/var/lib/netknownsthat/nut-backup-$(date +%Y%m%d-%H%M%S)
DISABLED="" INITD="" EDITED=""
T() { timeout 60 "$@"; }
rollback() {
  echo "ИБП не ответил ни с одним вариантом — возвращаю как было / no variant answered — rolling back"
  T systemctl disable --now nut-monitor nut-server >/dev/null 2>&1 || true
  T systemctl stop nut-driver.target >/dev/null 2>&1 || true
  T upsdrvctl stop >/dev/null 2>&1 || true
  if [ -d "$BACKUP/nut" ]; then rm -rf /etc/nut && cp -a "$BACKUP/nut" /etc/nut; else rm -f /etc/nut/ups.conf; fi
  for f in $EDITED; do cp -a "$BACKUP/autostart/$(echo "$f" | tr / _)" "$f" && echo "Возвращён / restored: $f"; done
  for u in $DISABLED; do T systemctl enable --now "$u" || true; done
  for u in $INITD; do "$u" start || true; command -v update-rc.d >/dev/null && update-rc.d "$(basename "$u")" enable || true; done
  if ! pgrep -x -i 'rupsd|upsilon|upsilond' >/dev/null 2>&1 && [ -s "$BACKUP/upsilon.ps" ]; then
    while read -r pid cmd; do
      [ -n "$cmd" ] || continue
      echo "Запускаю UPSilon снова / starting UPSilon again: $cmd"
      setsid nohup sh -c "$cmd" >/dev/null 2>&1 < /dev/null &
    done < "$BACKUP/upsilon.ps"
  fi
}
trap 'rc=$?; if [ $rc -ne 0 ]; then rollback; fi; exit $rc' EXIT

mkdir -p "$BACKUP/autostart"
if [ -d /etc/nut ]; then cp -a /etc/nut "$BACKUP/nut"; fi
echo "Резервная копия / backup: $BACKUP"

# UPSilon: что работало — запомнить (для отката), автозапуск — выключить.
pgrep -a -f -i 'upsilon|rupsd' 2>/dev/null | grep -v -e pgrep -e 'nkt:quiet' > "$BACKUP/upsilon.ps" || true
for a in $NKT_UPS_AUTOSTART; do
  case "$a" in
    /etc/init.d/*)
      echo "Выключаю автозапуск UPSilon / disabling UPSilon autostart: $a"
      "$a" stop || true
      command -v update-rc.d >/dev/null && update-rc.d "$(basename "$a")" disable || true
      INITD="$INITD $a" ;;
    *.service)
      echo "Выключаю службу UPSilon / disabling the UPSilon service: $(basename "$a")"
      T systemctl disable --now "$(basename "$a")" || true
      DISABLED="$DISABLED $(basename "$a")" ;;
    *)
      echo "Комментирую запуск UPSilon / commenting out UPSilon start: $a"
      cp -a "$a" "$BACKUP/autostart/$(echo "$a" | tr / _)"
      sed -i -E '/^[[:space:]]*#/!{/(upsilon|rupsd)/I s/^/# nkt: UPSilon disabled — /}' "$a"
      EDITED="$EDITED $a" ;;
  esac
done
for pid in $NKT_UPS_PIDS; do kill "$pid" 2>/dev/null && echo "Останавливаю UPSilon / stopping UPSilon: PID $pid" || true; done
pkill -x -i 'rupsd|upsilon|upsilond' 2>/dev/null || true
sleep 2
for pid in $NKT_UPS_PIDS; do kill -9 "$pid" 2>/dev/null || true; done

if ! command -v upsc >/dev/null 2>&1 || ! command -v upsdrvctl >/dev/null 2>&1; then
  echo "Ставлю пакет nut / installing the nut package"
  apt-get update
  apt-get install -y nut
fi
mkdir -p /etc/nut

PASS=$(head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)
echo 'MODE=standalone' > /etc/nut/nut.conf
printf 'LISTEN 127.0.0.1 3493\nLISTEN ::1 3493\n' > /etc/nut/upsd.conf
printf '[nkt-monitor]\n  password = %s\n  upsmon primary\n' "$PASS" > /etc/nut/upsd.users
{
  echo "MONITOR $NAME@localhost 1 nkt-monitor $PASS primary"
  echo 'SHUTDOWNCMD "/sbin/shutdown -h +0"'
  echo 'MINSUPPLIES 1'
} > /etc/nut/upsmon.conf

try() {
  drv=$1 port=$2 vid=$3 pid=$4
  {
    echo "# nkt: ИБП, найденный мастером «Подключить ИБП»"
    echo "[$NAME]"
    echo "  driver = $drv"
    echo "  port = $port"
    if [ -n "$vid" ]; then echo "  vendorid = $vid"; fi
    if [ -n "$pid" ]; then echo "  productid = $pid"; fi
    echo '  desc = "nkt"'
  } > /etc/nut/ups.conf
  chown root:nut /etc/nut/*.conf /etc/nut/upsd.users 2>/dev/null || true
  chmod 640 /etc/nut/*.conf /etc/nut/upsd.users
  echo "Пробую / trying: драйвер $drv, порт $port"
  T systemctl stop nut-server >/dev/null 2>&1 || true
  T systemctl stop nut-driver.target >/dev/null 2>&1 || true
  T upsdrvctl stop >/dev/null 2>&1 || true
  T systemctl daemon-reload || true
  T systemctl restart nut-driver.target 2>/dev/null || T systemctl restart "nut-driver@$NAME" 2>/dev/null || T upsdrvctl start || true
  T systemctl restart nut-server || true
  for i in $(seq 1 20); do
    if upsc "$NAME@localhost" ups.status >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  echo "— $drv на $port не ответил; что сказал драйвер / what the driver said:"
  journalctl -u "nut-driver@$NAME" -n 12 --no-pager -o cat 2>/dev/null | sed 's/^/    /' || true
  T systemctl stop nut-driver.target >/dev/null 2>&1 || true
  T upsdrvctl stop >/dev/null 2>&1 || true
  timeout 15 upsdrvctl -D start "$NAME" 2>&1 | tail -n 12 | sed 's/^/    /' || true
  T upsdrvctl stop >/dev/null 2>&1 || true
  return 1
}

ok=""
for c in $NKT_UPS_CANDIDATES; do
  IFS='|' read -r d p v q <<EOT
$c
EOT
  if try "$d" "$p" "$v" "$q"; then ok="$d $p"; break; fi
done
[ -n "$ok" ] || exit 1

T systemctl enable nut-server >/dev/null 2>&1 || true
if [ "$NKT_UPS_SHUTDOWN" = "1" ]; then
  T systemctl enable --now nut-monitor
  echo "Выключение хоста при садящейся батарее включено / host shutdown on low battery enabled"
else
  T systemctl disable --now nut-monitor >/dev/null 2>&1 || true
  sed -i 's/^MONITOR /# MONITOR /' /etc/nut/upsmon.conf
fi
echo "ИБП отвечает / the UPS answers ($ok):"
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
