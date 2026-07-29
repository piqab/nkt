# Сборка и проверка NetKnownsThat.
#
# Продакшен-бинарник собирается ТОЛЬКО в Linux-окружении — целью `build`,
# которая запускает компиляцию внутри контейнера golang. Это работает с любой
# рабочей машины и даёт один и тот же артефакт независимо от того, откуда его
# собрали. Нативная сборка (`build-dev`) предназначена только для разработки
# в режиме fixtures.

GO_IMAGE   ?= golang:1.26-alpine
NODE_IMAGE ?= node:22-alpine
GOARCH     ?= amd64
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS    := -s -w -X main.version=$(VERSION)
OUT        := dist/nkt

# Кэш модулей и npm переживает пересборки, иначе каждая тянет зависимости заново.
DOCKER_RUN = docker run --rm \
	-v "$(CURDIR)":/src -w /src \
	-v nkt-gomod:/go/pkg/mod \
	-v nkt-gocache:/root/.cache/go-build

.DEFAULT_GOAL := help

.PHONY: help
help: ## показать список целей
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: web
web: ## собрать веб-интерфейс (вшивается в бинарник)
	docker run --rm -v "$(CURDIR)":/src -w /src/web -v nkt-npm:/root/.npm $(NODE_IMAGE) \
		sh -c "npm ci && npm run build"

.PHONY: build
build: web ## собрать продакшен-бинарник для Linux (внутри контейнера)
	@mkdir -p dist
	$(DOCKER_RUN) -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=$(GOARCH) $(GO_IMAGE) \
		go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT) ./cmd/nkt
	@echo "готово: $(OUT) ($(VERSION), linux/$(GOARCH))"
	@file $(OUT) 2>/dev/null || true

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

.PHONY: install
install: ## установить на этот Linux-хост (запускать на самом хосте от root)
	@test "$$(uname -s)" = "Linux" || (echo "install выполняется только на Linux-хосте" && false)
	install -m 0755 $(OUT) /usr/local/bin/nkt
	install -d -m 0750 /etc/netknownsthat
	@test -f /etc/netknownsthat/nkt.env || install -m 0640 deploy/nkt.env.example /etc/netknownsthat/nkt.env
	install -m 0644 deploy/netknownsthat.service /etc/systemd/system/
	systemctl daemon-reload
	@echo "готово. Дальше: systemctl enable --now netknownsthat"

.PHONY: clean
clean: ## удалить артефакты сборки
	rm -rf dist nkt nkt.exe
