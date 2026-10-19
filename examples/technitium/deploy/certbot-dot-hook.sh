#!/bin/sh
# WARNING: пример использует сторонний репозиторий и образы — их безопасность и стабильность работы не гарантируются.
# WARNING: this example uses a third-party repository and images — their security and stability are not guaranteed.
# certbot --deploy-hook для DoT/DoQ Technitium: после выпуска и каждого
# продления сертификата сайта DOMAIN кладёт его в PFX (формат, который
# понимает Technitium) в каталог TLS стека. Technitium сам перечитывает
# файл меньше чем за минуту — перезапуск не нужен.
#
# Установка на хосте первичного сервера (DOMAIN — имя сайта DoH):
#   sudo install -m 0755 certbot-dot-hook.sh /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
#   sudo sed -i 's/^DOMAIN=.*/DOMAIN=dns.example.com/' /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
#   sudo RENEWED_LINEAGE=/etc/letsencrypt/live/dns.example.com /etc/letsencrypt/renewal-hooks/deploy/technitium-dot.sh
# Пароль PFX — в tls/pfx.pass рядом (создаётся один раз); его же — в
# консоли Technitium: Settings → Optional Protocols → TLS certificate.
set -eu

DOMAIN=dns.example.com
DIR=/srv/compose/technitium/tls

# certbot вызывает хук для каждого продлённого сертификата — нам нужен один.
case "${RENEWED_LINEAGE:-}" in
  */"$DOMAIN") ;;
  *) exit 0 ;;
esac

umask 077
mkdir -p "$DIR"
[ -s "$DIR/pfx.pass" ] || head -c 24 /dev/urandom | base64 > "$DIR/pfx.pass"
openssl pkcs12 -export -out "$DIR/dns.pfx.tmp" \
  -inkey "$RENEWED_LINEAGE/privkey.pem" -in "$RENEWED_LINEAGE/fullchain.pem" \
  -passout file:"$DIR/pfx.pass"
# Подмена целиком (mv): Technitium не прочтёт файл наполовину записанным.
mv "$DIR/dns.pfx.tmp" "$DIR/dns.pfx"
echo "technitium-dot: $DIR/dns.pfx обновлён из $RENEWED_LINEAGE"
