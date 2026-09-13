---
title: Установка на хост
---

# Установка на хост

nkt — один статический бинарник для Linux. Он ставится на хост, который
нужно видеть и править, работает как systemd-служба и слушает только
`127.0.0.1:8077`. Никаких зависимостей: ни базы, ни агентов, ни Docker.

::: tip Много хостов?
Если хостов несколько, ставьте [хаб](/guide/hub): он поставит nkt на каждый
сам и покажет их в одном окне.
:::

## 1. Бинарник

Готовые бинарники для `linux/amd64`, `linux/arm64` и `linux/arm` лежат в
[Releases](https://github.com/piqab/nkt/releases) вместе с `SHA256SUMS`:

```bash
# подставьте свою архитектуру и нужную версию
V=$(curl -fsSL https://api.github.com/repos/piqab/nkt/releases/latest | sed -n 's/.*"tag_name": *"\(.*\)".*/\1/p')
curl -fsSLO https://github.com/piqab/nkt/releases/download/$V/nkt-linux-amd64
curl -fsSLO https://github.com/piqab/nkt/releases/download/$V/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
chmod +x nkt-linux-amd64
sudo mv nkt-linux-amd64 /usr/local/bin/nkt
```

Сборка из исходников — в
[DEVELOPMENT.md](https://github.com/piqab/nkt/blob/main/DEVELOPMENT.md):
нужен только `make`.

## 2. Проверить, что хост читается

```bash
sudo nkt scan
```

Команда печатает найденные слушатели, контейнеры, правила firewall и
проблемы — то же, что покажет веб-интерфейс. Нужен root: `iptables-save`
и `systemctl` обычному пользователю недоступны.

## 3. Служба

Конфиг и systemd-юнит берутся прямо с GitHub, клонировать репозиторий не
нужно:

```bash
sudo install -d -m 0750 /etc/netknownsthat
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/nkt.env.example \
  | sudo install -m 0640 /dev/stdin /etc/netknownsthat/nkt.env
curl -fsSL https://raw.githubusercontent.com/piqab/nkt/main/deploy/netknownsthat.service \
  | sudo install -m 0644 /dev/stdin /etc/systemd/system/netknownsthat.service
sudo $EDITOR /etc/netknownsthat/nkt.env
sudo systemctl daemon-reload
sudo systemctl enable --now netknownsthat
sudo journalctl -u netknownsthat -n 30     # здесь будет пароль администратора
```

Пароль администратора печатается в журнал **один раз**, при первом
запуске. Чтобы задать его заранее — `NKT_BOOTSTRAP_ADMIN_PASSWORD` в
`nkt.env` до первого старта; потом пароли меняются командой `nkt passwd`.

## 4. Открыть в браузере

Служба слушает `127.0.0.1:8077`. Три способа добраться до неё:

- **SSH-туннель** — быстрее всего:
  ```bash
  ssh -L 8077:127.0.0.1:8077 user@host
  ```
  и открыть `http://127.0.0.1:8077`. Туннель отдаёт обычный HTTP, поэтому в
  `nkt.env` нужно `NKT_COOKIE_SECURE=false`.
- **Свой HTTPS** — `NKT_TLS_ENABLED=true`: nkt сам выпустит самоподписанный
  сертификат (имена — через `NKT_TLS_HOSTS`) или возьмёт ваш из
  `NKT_TLS_CERT`/`NKT_TLS_KEY`.
- **Обратный прокси** с TLS (nginx, Caddy) перед `127.0.0.1:8077`.

::: danger Доступ к nkt равносилен root на хосте
Он правит конфиги, управляет сервисами и меняет firewall. Не открывайте
его в интернет без отдельного слоя аутентификации.
:::

## Что дальше

- [Хаб](/guide/hub) — если хостов больше одного.
- [Возможности](/features) — что есть в каждом разделе.
