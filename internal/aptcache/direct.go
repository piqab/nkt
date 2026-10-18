package aptcache

import (
	"regexp"
	"strings"
)

// Пакеты через кэш хаба: apt хоста ходит через 127.0.0.1:3142. Кэш хаба
// бывает не в силах достать пакет, который хосту доступен, — например,
// внутреннее зеркало хостинга (mirror.<провайдер>) с хаба не видно, и кэш
// отвечает 502. Тогда установка повторяется мимо кэша, напрямую.

// DirectOpts — параметры apt-get мимо кэша хаба (и автоопределения прокси).
const DirectOpts = "-o Acquire::http::Proxy-Auto-Detect= -o Acquire::http::Proxy=DIRECT -o Acquire::https::Proxy=DIRECT"

var proxyFailRe = regexp.MustCompile(`127\.0\.0\.1:?\s?3142|\b50[234]\s+(Bad Gateway|Service Unavailable|Gateway Time-?out)`)

// ProxyFailed — вывод apt говорит, что не справился кэш (прокси) хаба.
func ProxyFailed(out string) bool { return proxyFailRe.MatchString(out) }

// Direct — команда с apt-get мимо кэша хаба.
func Direct(cmd string) string {
	return strings.ReplaceAll(cmd, "apt-get ", "apt-get "+DirectOpts+" ")
}

// shellFunc — nkt_apt вместо apt-get в сценариях: вывод идёт как обычно
// (tee), а если apt-get упал из-за кэша хаба — повтор мимо него.
const shellFunc = `nkt_apt() {
  _nkt_out=$(mktemp); _nkt_rc=$(mktemp)
  { apt-get "$@" 2>&1; echo $? > "$_nkt_rc"; } | tee "$_nkt_out"
  _nkt_code=$(cat "$_nkt_rc")
  if [ "$_nkt_code" != 0 ] && grep -qE '127\.0\.0\.1:? ?3142|50[234] +(Bad Gateway|Service Unavailable|Gateway Time)' "$_nkt_out"; then
    echo "nkt: hub package cache could not fetch this — retrying directly"
    apt-get ` + DirectOpts + ` "$@"; _nkt_code=$?
  fi
  rm -f "$_nkt_out" "$_nkt_rc"
  return "$_nkt_code"
}
`

// WrapScript — сценарий, где apt-get при отказе кэша хаба повторяется
// напрямую. Сценарий без apt-get не меняется.
func WrapScript(script string) string {
	if !strings.Contains(script, "apt-get ") {
		return script
	}
	return shellFunc + strings.ReplaceAll(script, "apt-get ", "nkt_apt ")
}
