#!/bin/sh
# Вызов вебхука конвейера хаба nkt с подписью nkt — из любого CI или
# вручную после локальной сборки образа.
#
#   NKT_HOOK_URL     адрес из окна «Вебхук» конвейера
#   NKT_HOOK_SECRET  секрет подписи оттуда же
#
#   sh scripts/nkt-hook.sh <тег образа> [коммит] [ветка]
#
# Хаб выложит ветку (по умолчанию main) на этом коммите с этим тегом
# образа. Подпись — HMAC-SHA256 строки «<отметка времени>.<тело>»;
# отметка не старше 5 минут, повтор той же подписи хаб отвергнет.
# Нужны sh, openssl и curl.
set -eu

TAG=${1:?usage: nkt-hook.sh <image tag> [commit] [branch]}
COMMIT=${2:-}
BRANCH=${3:-main}
: "${NKT_HOOK_URL:?NKT_HOOK_URL is not set}"
: "${NKT_HOOK_SECRET:?NKT_HOOK_SECRET is not set}"

BODY=$(printf '{"ref":"refs/heads/%s","commit":"%s","tag":"%s"}' "$BRANCH" "$COMMIT" "$TAG")
TS=$(date +%s)
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$NKT_HOOK_SECRET" -hex | sed 's/^.* //')
curl -fsS -X POST "$NKT_HOOK_URL" \
  -H "Content-Type: application/json" \
  -H "X-NKT-Timestamp: $TS" \
  -H "X-NKT-Signature: $SIG" \
  -d "$BODY"
echo
