# Сборка и проверка NetKnownsThat.
#
# Продакшен-бинарник собирается ТОЛЬКО в Linux-окружении — целью `build`.
# Если есть Docker, компиляция идёт внутри контейнера golang — это работает с
# любой рабочей машины и даёт один и тот же артефакт независимо от того,
# откуда его собрали. Если Docker не найден, `build`/`web` сами переключаются
# на цель `native-build`: она собирает прямо на хосте и, если там нет
# подходящего Go (1.25+) или Node (20+), спрашивает подтверждение и ставит их
# в $HOME/.local — без sudo, ничего системного не трогая. Результат — dist/nkt
# — один и тот же независимо от того, какой из путей сработал.
# Нативная сборка (`build-dev`) — отдельная цель, только для разработки в
# режиме fixtures, без всего вышеперечисленного.

# native-build использует read -p, local и параметрическое расширение —
# всё это bash, не гарантированный POSIX sh (на Debian/Ubuntu /bin/sh — это
# dash, где `read -p` не поддерживается). Закрепляем shell для всего файла,
# остальным целям это не мешает — они используют только простой POSIX-синтаксис.
SHELL := bash

GO_IMAGE   ?= golang:1.26-alpine
NODE_IMAGE ?= node:22-alpine
GOARCH     ?= amd64
# Semver, bumped by hand in the VERSION file — not derived from git tags:
# this project doesn't tag releases, and the hub's own "обновить" button
# (internal/hub, web/src/pages/Hosts.tsx) needs a number it can actually
# compare release-to-release, which a git-describe hash cannot give it.
VERSION    ?= $(shell tr -d '[:space:]' < VERSION 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X main.version=$(VERSION)
OUT        := dist/nkt

# Кэш модулей и npm переживает пересборки, иначе каждая тянет зависимости заново.
DOCKER_RUN = docker run --rm \
	-v "$(CURDIR)":/src -w /src \
	-v nkt-gomod:/go/pkg/mod \
	-v nkt-gocache:/root/.cache/go-build

# --------------------------------------------------------- native-build (no Docker)

UNAME_M := $(shell uname -m 2>/dev/null)
ifeq ($(UNAME_M),x86_64)
NATIVE_GO_ARCH   := amd64
NATIVE_NODE_ARCH := x64
else ifeq ($(UNAME_M),aarch64)
NATIVE_GO_ARCH   := arm64
NATIVE_NODE_ARCH := arm64
else
NATIVE_GO_ARCH   := $(UNAME_M)
NATIVE_NODE_ARCH := $(UNAME_M)
endif

MIN_GO_MINOR    := 25
MIN_NODE_MAJOR  := 20
LOCAL_ROOT      := $(HOME)/.local
LOCAL_GO_DIR    := $(LOCAL_ROOT)/go
LOCAL_NODE_DIR  := $(LOCAL_ROOT)/node

.DEFAULT_GOAL := help

.PHONY: help
help: ## показать список целей
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: web
web: ## собрать веб-интерфейс (вшивается в бинарник; без Docker — через native-build)
	@if command -v docker >/dev/null 2>&1; then \
		docker run --rm -v "$(CURDIR)":/src -w /src/web -v nkt-npm:/root/.npm $(NODE_IMAGE) \
			sh -c "npm ci && npm run build"; \
	else \
		echo "docker не найден — собираю через native-build (заодно соберёт и dist/nkt)"; \
		$(MAKE) native-build; \
	fi

.PHONY: build
build: ## собрать продакшен-бинарник для Linux (Docker, если есть; иначе — native-build); увеличивает минорную версию в VERSION
	@if command -v docker >/dev/null 2>&1; then \
		$(MAKE) build-docker; \
	else \
		echo "docker не найден — собираю напрямую на хосте (native-build)"; \
		$(MAKE) native-build; \
	fi

# Разделено на отдельную цель (а не просто ветку build) исключительно из-за
# bump-version: он должен быть настоящим prerequisite (см. его комментарий),
# а из двух путей — через Docker и через native-build — за один запуск
# make build выполняется только один, так что версия увеличивается ровно
# один раз независимо от того, какой путь сработал.
.PHONY: build-docker
build-docker: bump-version web
	@mkdir -p dist; \
	$(DOCKER_RUN) -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=$(GOARCH) $(GO_IMAGE) \
		go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT) ./cmd/nkt; \
	echo "готово: $(OUT) ($(VERSION), linux/$(GOARCH))"; \
	file $(OUT) 2>/dev/null || true

# Prerequisite, а не $(MAKE) bump-version внутри рецепта build-docker/
# native-build: у обеих целей рецепт — один составной shell-скрипт (из-за \
# продолжений строк), и Make подставляет $(VERSION)/$(LDFLAGS) в него ДО
# того, как этот скрипт вообще начинает выполняться — вызов bump-version
# где-то в середине того же рецепта не успел бы повлиять на уже
# подставленные более ранние или поздние ссылки на $(VERSION) в нём же.
# Настоящий prerequisite гарантированно отрабатывает раньше, чем Make
# вообще начинает подставлять переменные в рецепт зависимой цели.
.PHONY: bump-version
bump-version: ## увеличить минорную версию в файле VERSION (запускается автоматически при build/native-build)
	@current=$$(tr -d '[:space:]' < VERSION 2>/dev/null || echo 0.0.0); \
	major=$$(echo "$$current" | cut -d. -f1); \
	minor=$$(echo "$$current" | cut -d. -f2); \
	next="$$major.$$((minor + 1)).0"; \
	echo "$$next" > VERSION; \
	echo "версия: $$current -> $$next"

# Весь рецепт — одна логическая строка Make (каждая физическая строка
# заканчивается \), поэтому Make передаёт его целиком одному вызову shell —
# переменные, функции (confirm/go_ok/node_ok) и export PATH переживают
# переход между строками, как в обычном bash-скрипте. Тот же приём, что уже
# используется в build/web.
.PHONY: native-build
native-build: bump-version ## собрать без Docker: сама проверит и, если нужно, поставит Go/Node; увеличивает минорную версию в VERSION
	@set -euo pipefail; \
	export PATH="$(LOCAL_GO_DIR)/bin:$(LOCAL_NODE_DIR)/bin:$$PATH"; \
	confirm() { \
		read -r -p "$$1 [y/N] " reply; \
		case "$$reply" in \
			[yY]|[yY][eE][sS]|[дД]|[дД][аА]) return 0 ;; \
			*) return 1 ;; \
		esac; \
	}; \
	go_ok() { \
		command -v go >/dev/null 2>&1 || return 1; \
		local ver major minor; \
		ver=$$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | tr -d 'go'); \
		major=$${ver%%.*}; minor=$${ver##*.}; \
		[ "$$major" -gt 1 ] && return 0; \
		[ "$$major" -eq 1 ] && [ "$$minor" -ge $(MIN_GO_MINOR) ]; \
	}; \
	if go_ok; then \
		echo "Go: $$(go version) — подходит"; \
	else \
		if command -v go >/dev/null 2>&1; then \
			echo "Найден $$(go version), но нужен 1.$(MIN_GO_MINOR)+"; \
		else \
			echo "Go не найден"; \
		fi; \
		echo "Будет установлен в $(LOCAL_GO_DIR) (последняя версия с go.dev, без sudo)."; \
		if confirm "Установить Go?"; then \
			GOVER=$$(curl -fsSL "https://go.dev/VERSION?m=text" | head -1); \
			echo "Скачиваю $$GOVER для linux-$(NATIVE_GO_ARCH)…"; \
			mkdir -p "$(LOCAL_ROOT)"; \
			curl -fsSL "https://go.dev/dl/$${GOVER}.linux-$(NATIVE_GO_ARCH).tar.gz" -o /tmp/nkt-go.tar.gz; \
			rm -rf "$(LOCAL_GO_DIR)"; \
			tar -C "$(LOCAL_ROOT)" -xzf /tmp/nkt-go.tar.gz; \
			rm -f /tmp/nkt-go.tar.gz; \
			echo "Установлено: $$(go version)"; \
		else \
			echo "Отказ — без Go сборка невозможна." >&2; \
			exit 1; \
		fi; \
	fi; \
	node_ok() { \
		command -v npm >/dev/null 2>&1 || return 1; \
		local major; \
		major=$$(node -v | tr -d 'v' | cut -d. -f1); \
		[ "$$major" -ge $(MIN_NODE_MAJOR) ]; \
	}; \
	if node_ok; then \
		echo "Node: $$(node -v) — подходит"; \
	else \
		if command -v node >/dev/null 2>&1; then \
			echo "Найден $$(node -v), но нужен $(MIN_NODE_MAJOR)+"; \
		else \
			echo "Node/npm не найдены"; \
		fi; \
		echo "Будет установлен в $(LOCAL_NODE_DIR) (последняя версия с nodejs.org, без sudo)."; \
		if confirm "Установить Node?"; then \
			NODEVER=$$(curl -fsSL "https://nodejs.org/dist/index.tab" | sed -n '2p' | cut -f1); \
			echo "Скачиваю $$NODEVER для linux-$(NATIVE_NODE_ARCH)…"; \
			mkdir -p "$(LOCAL_ROOT)"; \
			curl -fsSL "https://nodejs.org/dist/$${NODEVER}/node-$${NODEVER}-linux-$(NATIVE_NODE_ARCH).tar.xz" -o /tmp/nkt-node.tar.xz; \
			rm -rf "$(LOCAL_NODE_DIR)"; \
			mkdir -p "$(LOCAL_NODE_DIR)"; \
			tar -C "$(LOCAL_NODE_DIR)" --strip-components=1 -xJf /tmp/nkt-node.tar.xz; \
			rm -f /tmp/nkt-node.tar.xz; \
			echo "Установлено: $$(node -v)"; \
		else \
			echo "Отказ — без Node сборка невозможна." >&2; \
			exit 1; \
		fi; \
	fi; \
	echo "Собираю веб-интерфейс…"; \
	( cd web && npm ci && npm run build ); \
	echo "Собираю бинарник…"; \
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT) ./cmd/nkt; \
	echo; \
	echo "готово: $$($(OUT) version)"; \
	if [ -d "$(LOCAL_GO_DIR)" ] || [ -d "$(LOCAL_NODE_DIR)" ]; then \
		echo; \
		echo "Go/Node из $(LOCAL_ROOT) следующий make build подхватит сам — но чтобы"; \
		echo "вызывать go/npm напрямую вне make, добавьте в ~/.bashrc (или ~/.profile):"; \
		echo "  export PATH=\"$(LOCAL_GO_DIR)/bin:$(LOCAL_NODE_DIR)/bin:\$$PATH\""; \
	fi

.PHONY: build-dev
build-dev: ## собрать бинарник для текущей ОС — ТОЛЬКО для разработки в режиме fixtures
	go build -ldflags "-X main.version=$(VERSION)-dev" -o nkt ./cmd/nkt

.PHONY: test
test: ## прогнать тесты Go и проверку типов фронтенда
	go test ./...
	cd web && npm run typecheck

.PHONY: check
check: ## тесты плюс vet и проверка форматирования
	go vet ./...
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "нужен gofmt -w" && false)
	$(MAKE) test

.PHONY: stand
stand: ## поднять проверочный стенд с настоящими nginx, haproxy и docker
	docker compose -f stand/docker-compose.yml up -d --build
	@echo "дашборд: http://127.0.0.1:8077   пароль: docker logs nkt"

.PHONY: stand-down
stand-down: ## остановить стенд и удалить его тома
	docker compose -f stand/docker-compose.yml down -v

.PHONY: hub
hub: ## поднять управляющий центр (nkt hub) в Docker
	VERSION=$(VERSION) docker compose -f deploy/docker-compose.hub.yml up -d --build
	@echo "хаб: http://127.0.0.1:8443   пароль: docker compose -f deploy/docker-compose.hub.yml logs hub"

.PHONY: hub-down
hub-down: ## остановить хаб и удалить его тома
	docker compose -f deploy/docker-compose.hub.yml down -v

.PHONY: install
install: ## установить на этот Linux-хост (запускать на самом хосте от root)
	@test "$$(uname -s)" = "Linux" || (echo "install выполняется только на Linux-хосте" && false)
	install -m 0755 $(OUT) /usr/local/bin/nkt
	install -d -m 0750 /etc/netknownsthat
	@test -f /etc/netknownsthat/nkt.env || install -m 0640 deploy/nkt.env.example /etc/netknownsthat/nkt.env
	install -m 0644 deploy/netknownsthat.service /etc/systemd/system/
	systemctl daemon-reload
	@echo "готово. Дальше: systemctl enable --now netknownsthat"

.PHONY: hub-install
hub-install: ## установить хаб на этот Linux-хост как systemd-сервис, без Docker (запускать от root)
	@test "$$(uname -s)" = "Linux" || (echo "hub-install выполняется только на Linux-хосте" && false)
	install -m 0755 $(OUT) /usr/local/bin/nkt
	install -d -m 0750 /etc/netknownsthat
	@test -f /etc/netknownsthat/hub.env || install -m 0640 deploy/hub.env.example /etc/netknownsthat/hub.env
	install -m 0644 deploy/netknownsthat-hub.service /etc/systemd/system/
	systemctl daemon-reload
	@echo "готово. Дальше: systemctl enable --now netknownsthat-hub"

.PHONY: clean
clean: ## удалить артефакты сборки
	rm -rf dist nkt nkt.exe
