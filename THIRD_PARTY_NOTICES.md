# Сторонние компоненты NetKnownsThat

English version: [THIRD_PARTY_NOTICES.en.md](THIRD_PARTY_NOTICES.en.md).

Код NetKnownsThat распространяется по лицензии MIT (файл
[LICENSE](LICENSE)). В сборку `nkt` входят сторонние компоненты со своими
лицензиями. Лицензия MIT на них не распространяется: для каждого
компонента действует его собственная лицензия.

## Клиенты экрана виртуальных машин

### spice-html5 — LGPL-3.0-or-later

- **Что это:** клиент SPICE в браузере. Окно «Экран» открывает через него
  машины libvirt со SPICE-графикой и виртуальные машины LXD.
- **Где лежит:** `web/public/vendor/spice-html5/`. В бинарнике `nkt` это
  отдельные файлы по адресу `/vendor/spice-html5/`.
- **Исходный код:** <https://gitlab.freedesktop.org/spice/spice-html5>,
  снимок ветки master от 2026-09-07.
- **Изменения:** файлы не изменены и не минифицированы. В сборку
  интерфейса nkt они не входят, браузер загружает их отдельным
  динамическим импортом только при открытии окна SPICE.
- **Лицензия:** GNU LGPL версии 3 или новее. Тексты лицензии лежат рядом
  с файлами: `COPYING` (GPL-3.0) и `COPYING.LESSER` (LGPL-3.0).
- **Замена библиотеки.** Чтобы подставить свою или изменённую версию,
  замените файлы в `web/public/vendor/spice-html5/` и пересоберите `nkt`
  (`make build`). Весь остальной исходный код nkt открыт по MIT, так что
  такая замена всегда возможна.

Вместе со spice-html5 в каталоге `thirdparty/` поставляются:

| Файлы | Автор | Лицензия |
|---|---|---|
| `jsbn.js`, `prng4.js`, `rng.js`, `rsa.js` | Tom Wu, 2003–2005 | MIT-подобная, текст в заголовке каждого файла |
| `sha1.js` | Paul Johnston и соавторы, 1998–2009 | BSD-3-Clause, текст в заголовке файла |

### noVNC 1.7.0 — MPL-2.0

- **Что это:** клиент VNC в браузере для окна «Экран» машин libvirt.
- **Как поставляется:** пакет npm `@novnc/novnc` без изменений, собранный
  в JavaScript-бандл интерфейса.
- **Исходный код:** <https://github.com/novnc/noVNC>, тег `v1.7.0`.
  Файлы, покрытые MPL-2.0, остаются под MPL-2.0.
- **Текст лицензии:** <https://mozilla.org/MPL/2.0/>.

## Сервер (Go)

| Модуль | Версия | Лицензия |
|---|---|---|
| github.com/coder/websocket | v1.8.15 | ISC |
| github.com/creack/pty | v1.1.24 | MIT |
| github.com/gdamore/tcell/v2 | v2.13.10 | Apache-2.0 |
| github.com/go-chi/chi/v5 | v5.3.2 | MIT |
| github.com/haproxytech/config-parser/v5 | v5.1.6 | Apache-2.0 |
| github.com/hashicorp/yamux | v0.1.2 | MPL-2.0 |
| github.com/nginxinc/nginx-go-crossplane | v0.4.89 | Apache-2.0 |
| github.com/pkg/sftp | v1.13.11 | BSD-2-Clause |
| github.com/rivo/tview | v0.42.0 | MIT |
| golang.org/x/crypto, x/net, x/term | v0.57.0, v0.58.0, v0.46.0 | BSD-3-Clause |
| gopkg.in/yaml.v3 | v3.0.1 | MIT и Apache-2.0, NOTICE: Copyright 2011-2016 Canonical Ltd. |
| modernc.org/sqlite | v1.59.0 | BSD-3-Clause; сама SQLite — общественное достояние |

`hashicorp/yamux` под MPL-2.0 используется без изменений. Его исходный
код: <https://github.com/hashicorp/yamux>, тег `v0.1.2`.

Косвенные зависимости перечислены в `go.mod` и `go.sum`. Их лицензии
лежат в кэше модулей (`go env GOMODCACHE`) рядом с исходным кодом.

## Интерфейс (npm)

| Пакет | Версия | Лицензия |
|---|---|---|
| react, react-dom | 18.3.1 | MIT |
| antd | 6.6.4 | MIT |
| @ant-design/icons | 6.3.4 | MIT |
| i18next | 26.4.2 | MIT |
| react-i18next | 17.0.14 | MIT |
| @xterm/xterm | 5.5.0 | MIT |
| @xterm/addon-canvas, -fit, -search, -unicode11, -web-links | 0.7–0.16 | MIT |
| @novnc/novnc | 1.7.0 | MPL-2.0, см. выше |

Полный список пакетов с версиями — в `web/package-lock.json`.
