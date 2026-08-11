#!/usr/bin/env bash
# Собирает dist/nkt без Docker. Проверяет версии Go и Node, и если чего-то
# нет или версия старая — СПРАШИВАЕТ перед установкой (в $HOME/.local, без
# sudo, ничего не трогает системно). Запускать из корня репозитория:
#   bash bootstrap-build.sh
set -euo pipefail

MIN_GO_MAJOR=1
MIN_GO_MINOR=25
MIN_NODE_MAJOR=20

LOCAL_ROOT="$HOME/.local"
GO_DIR="$LOCAL_ROOT/go"
NODE_DIR="$LOCAL_ROOT/node"

case "$(uname -m)" in
  x86_64|amd64) GO_ARCH=amd64; NODE_ARCH=x64 ;;
  aarch64|arm64) GO_ARCH=arm64; NODE_ARCH=arm64 ;;
  *) echo "Неизвестная архитектура: $(uname -m)" >&2; exit 1 ;;
esac

confirm() {
  read -r -p "$1 [y/N] " reply
  case "$reply" in
    [yY]|[yY][eE][sS]|[дД]|[дД][аА]) return 0 ;;
    *) return 1 ;;
  esac
}

# --------------------------------------------------------------------- Go

go_ok() {
  command -v go >/dev/null 2>&1 || return 1
  local ver major minor
  ver=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | tr -d 'go')
  major=${ver%%.*}
  minor=${ver##*.}
  [ "$major" -gt "$MIN_GO_MAJOR" ] && return 0
  [ "$major" -eq "$MIN_GO_MAJOR" ] && [ "$minor" -ge "$MIN_GO_MINOR" ]
}

if go_ok; then
  echo "Go: $(go version) — подходит"
else
  if command -v go >/dev/null 2>&1; then
    echo "Найден $(go version), но нужен $MIN_GO_MAJOR.$MIN_GO_MINOR+"
  else
    echo "Go не найден"
  fi
  echo "Будет установлен в $GO_DIR (последняя версия с go.dev, без sudo)."
  if confirm "Установить Go?"; then
    GOVER=$(curl -fsSL "https://go.dev/VERSION?m=text" | head -1)
    echo "Скачиваю $GOVER для linux-$GO_ARCH…"
    mkdir -p "$LOCAL_ROOT"
    curl -fsSL "https://go.dev/dl/${GOVER}.linux-${GO_ARCH}.tar.gz" -o /tmp/go.tar.gz
    rm -rf "$GO_DIR"
    tar -C "$LOCAL_ROOT" -xzf /tmp/go.tar.gz
    rm -f /tmp/go.tar.gz
    export PATH="$GO_DIR/bin:$PATH"
    echo "Установлено: $(go version)"
  else
    echo "Отказ — без Go сборка невозможна." >&2
    exit 1
  fi
fi

# ------------------------------------------------------------------- Node

node_ok() {
  command -v npm >/dev/null 2>&1 || return 1
  local major
  major=$(node -v | tr -d 'v' | cut -d. -f1)
  [ "$major" -ge "$MIN_NODE_MAJOR" ]
}

if node_ok; then
  echo "Node: $(node -v) — подходит"
else
  if command -v node >/dev/null 2>&1; then
    echo "Найден $(node -v), но нужен $MIN_NODE_MAJOR+"
  else
    echo "Node/npm не найдены"
  fi
  echo "Будет установлен в $NODE_DIR (последняя версия с nodejs.org, без sudo)."
  if confirm "Установить Node?"; then
    NODEVER=$(curl -fsSL "https://nodejs.org/dist/index.tab" | sed -n '2p' | cut -f1)
    echo "Скачиваю $NODEVER для linux-$NODE_ARCH…"
    mkdir -p "$LOCAL_ROOT"
    curl -fsSL "https://nodejs.org/dist/${NODEVER}/node-${NODEVER}-linux-${NODE_ARCH}.tar.xz" -o /tmp/node.tar.xz
    rm -rf "$NODE_DIR"
    mkdir -p "$NODE_DIR"
    tar -C "$NODE_DIR" --strip-components=1 -xJf /tmp/node.tar.xz
    rm -f /tmp/node.tar.xz
    export PATH="$NODE_DIR/bin:$PATH"
    echo "Установлено: $(node -v)"
  else
    echo "Отказ — без Node сборка невозможна." >&2
    exit 1
  fi
fi

# -------------------------------------------------------------------- Build

echo "Собираю веб-интерфейс…"
( cd web && npm ci && npm run build )

echo "Собираю бинарник…"
VERSION=$(git describe --tags --always 2>/dev/null || echo dev)
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o dist/nkt ./cmd/nkt

echo
echo "Готово: $(dist/nkt version)"
cat <<EOF

Если Go/Node ставились в $LOCAL_ROOT, добавьте в ~/.bashrc (или ~/.profile),
чтобы не искать их заново каждый раз:
  export PATH="$GO_DIR/bin:$NODE_DIR/bin:\$PATH"
EOF
