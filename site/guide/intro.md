---
title: Что такое nkt
---

# Что такое nkt

**NetKnownsThat (nkt)** смотрит на Linux-хост и отвечает на вопрос,
который обычно решается полудюжиной команд вручную: **что на самом деле
слушает сеть здесь, совпадает ли это с конфигурацией и что сломано.**

Он разбирает nginx, haproxy, Caddy, docker/compose, podman, LXD, libvirt,
iptables, ufw и firewalld и сверяет прочитанное с тем, что реально
происходит на машине: вывод `ss`, счётчики пакетов, живые контейнеры,
настоящее TLS-соединение к сокету сервиса. Расхождения становятся списком
проблем, связи между конфигами — картой ресурсов, история проверок —
расписанием доступности. И всё это тут же чинится: редактор конфигов с
проверкой и автооткатом, сервисы и контейнеры, правила firewall,
сертификаты.

Один статический бинарник (~25 МБ), четыре режима:

```
nkt          веб-панель и фоновый сбор данных
nkt tui      терминальный интерфейс для работы по SSH
nkt scan     разовая проверка, код выхода 2 при критичных находках
nkt hub      центр управления многими хостами
```

Ни Python, ни Node, ни отдельных файлов на хосте не нужно: веб-интерфейс
вшит в бинарник.

## Быстрый старт

**Один хост** — бинарник, служба, туннель:

```bash
V=$(curl -fsSL https://api.github.com/repos/piqab/nkt/releases/latest | sed -n 's/.*"tag_name": *"\(.*\)".*/\1/p')
curl -fsSL -o nkt https://github.com/piqab/nkt/releases/download/$V/nkt-linux-amd64 && sudo install -m 0755 nkt /usr/local/bin/nkt
sudo nkt scan
```

Дальше — [установка службы](/guide/getting-started#_3-служба).

**Много хостов** — хаб в Docker:

```bash
curl -fsSLO https://raw.githubusercontent.com/piqab/nkt/main/deploy/docker-compose.hub.release.yml
docker compose -f docker-compose.hub.release.yml up -d
docker compose -f docker-compose.hub.release.yml logs hub   # пароль администратора
```

Хаб откроется на `http://127.0.0.1:8443`; подробности —
[установка хаба](/guide/install-hub).

**Посмотреть без установки** — снапшот настоящего сервера с
заложенными проблемами, работает на Linux, macOS и Windows:

```bash
git clone https://github.com/piqab/nkt.git && cd nkt
make build-dev
NKT_MODE=fixtures ./nkt        # http://127.0.0.1:8077, пароль — в консоли
```

Режим `fixtures` ничего не читает с вашей машины и ничего в ней не
трогает: панель сразу покажет 25 находок — Redis, открытый всему миру,
конфликт порта, контейнер в цикле перезапуска, просроченный TLS. Пробы и
метрики там симулируются (в интерфейсе — баннер, в API —
`"simulated": true`), при первом запуске засеивается 14 дней истории;
отключается `NKT_DEMO_BACKFILL=false`. На Windows и macOS этот режим
включается сам.

## Куда дальше

- [Установка на хост](/guide/getting-started) и
  [установка хаба](/guide/install-hub).
- [Порты и доступ](/guide/ports) — что открывать наружу и что нет.
- [Руководство по разделам](/guide/overview) — что есть на каждой
  странице.
- [Выкладки](/guide/hub-deploy) — приложения из Git в кластеры и на
  хосты, [примеры CI/CD](/guide/cicd-examples).
- [Справочник](/guide/reference-config) — все переменные, безопасность,
  API, ограничения, решение проблем.
