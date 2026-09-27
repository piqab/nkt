#!/bin/sh
# certbot deploy-hook для nkt-edge: свежий сертификат домена вебхуков.
# Без прокси — копия в /etc/nkt-edge/tls для службы nkt-edge (она
# перечитывает файлы сама); за nginx — перезагрузка nginx.
set -e
env=/etc/nkt-edge/edge.env
domain=$(sed -n 's/^EDGE_DOMAIN=//p' "$env")
[ -n "$domain" ] && [ "$RENEWED_LINEAGE" = "/etc/letsencrypt/live/$domain" ] || exit 0
if grep -q '^EDGE_PROXY_ADDR=' "$env"; then
    if command -v nginx >/dev/null && nginx -t -q; then
        systemctl reload nginx
    fi
    exit 0
fi
install -d -m 0750 -g nkt-edge /etc/nkt-edge/tls
install -m 0640 -g nkt-edge "$RENEWED_LINEAGE/fullchain.pem" /etc/nkt-edge/tls/fullchain.pem
install -m 0640 -g nkt-edge "$RENEWED_LINEAGE/privkey.pem" /etc/nkt-edge/tls/privkey.pem
