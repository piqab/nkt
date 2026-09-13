#!/usr/bin/env python3
"""Скриншоты интерфейса для сайта (site/public/screens/{ru,en}).

Снимает страницы через Chrome DevTools Protocol с уже запущенного
headless-браузера — тем же способом, что и ручные проверки интерфейса:
никакой зависимости, кроме python3 и самого браузера.

    chrome --headless --remote-debugging-port=9350 about:blank &
    python3 scripts/screenshots.py --cdp 9350 \
        --host http://localhost:8077 --host-login admin:пароль \
        --hub  http://127.0.0.1:8112 --hub-login  admin:пароль

Хост — nkt в режиме fixtures (демо-данные из fixtures/host), хаб —
nkt hub с парой заведённых хостов. Каждая страница снимается на двух
языках светлой темой, 1400×900. Список экранов — SCREENS ниже: имя файла,
откуда (host/hub), путь и, если нужно, действие на странице (JS) — так
открываются вкладки и разделы хаба, которых нет в адресе.
"""
import argparse
import base64
import json
import os
import socket
import struct
import sys
import time
import urllib.request

# name, where, path, action (JS после загрузки; помощники — в HELPERS).
# Разделы, которым в fixtures нечего показать (задания, логи,
# уязвимости, пакеты, системные настройки), не снимаются.
SCREENS = [
    ('overview', 'host', '/', ''),
    ('findings', 'host', '/findings', ''),
    ('topology', 'host', '/topology', ''),
    ('availability', 'host', '/availability', ''),
    ('usage', 'host', '/usage', ''),
    ('audit', 'host', '/audit', ''),
    ('services', 'host', '/services', ''),
    ('containers', 'host', '/containers', ''),
    ('vms', 'host', '/vms', ''),
    ('profiles', 'host', '/profiles', ''),
    ('configs', 'host', '/configs', ''),
    ('configs-editor', 'host', '/configs', "clickText('button', /^\\/etc\\/nginx\\/nginx\\.conf/)"),
    ('interfaces', 'host', '/interfaces', ''),
    ('firewall', 'host', '/firewall', ''),
    ('certificates', 'host', '/certificates', ''),
    ('users', 'host', '/users', ''),
    ('hub-hosts', 'hub', '/', ''),
    ('hub-alerts', 'hub', '/', "menu({ru: 'Оповещения', en: 'Alerts'})"),
    ('hub-profiles', 'hub', '/', "menu({ru: 'Профили', en: 'Profiles'}); await sleep(800); clickText('button', /^web-base/)"),
    ('hub-scripts', 'hub', '/', "menu({ru: 'Профили', en: 'Profiles'}); await sleep(800); tab({ru: 'Сценарии', en: 'Scripts'}); await sleep(800); clickText('button', /^new-web-host/)"),
    ('hub-script-scheme', 'hub', '/', "menu({ru: 'Профили', en: 'Profiles'}); await sleep(800); tab({ru: 'Сценарии', en: 'Scripts'}); await sleep(800); clickText('button', /^new-web-host/); await sleep(800); tab({ru: 'Схема', en: 'Scheme'})"),
    ('hub-about', 'hub', '/', "menu({ru: 'О системе', en: 'About'})"),
]

# Помощники, доступные действию: клик по пункту меню/вкладке/тексту.
HELPERS = """
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const LANG = %(lang)r;
const pick = (m) => (typeof m === 'string' ? m : m[LANG]);
const clickText = (sel, re) => {
  const e = [...document.querySelectorAll(sel)].find((e) => re.test(e.textContent.trim()));
  if (e) e.click();
  return !!e;
};
const menu = (m) => clickText('.ant-menu-item', new RegExp('^' + pick(m)));
const tab = (m) => clickText('.ant-tabs-tab-btn', new RegExp('^' + pick(m) + '$'));
"""

# Перед снимком: убрать плашки режима fixtures — на сайте показывается
# интерфейс, а не оговорки стенда — и снять фокус с поля ввода.
CLEANUP = """
(() => {
  const re = /fixtures|снапшот|симуляци|simulat|snapshot mode/i;
  for (const el of document.querySelectorAll('.ant-alert, .banner, [class*="banner"]')) {
    if (re.test(el.textContent)) el.remove();
  }
  for (const el of document.querySelectorAll('.brand-sub')) {
    if (re.test(el.textContent)) el.textContent = el.textContent.replace(/\s*·\s*(режим|mode)\s*fixtures/i, '');
  }
  if (document.activeElement) document.activeElement.blur();
})()
"""


class WS:
    """Минимальный клиент websocket: CDP этого хватает."""

    def __init__(self, url):
        rest = url[5:]
        hostport, path = rest.split('/', 1)
        host, port = hostport.split(':')
        self.s = socket.create_connection((host, int(port)))
        key = base64.b64encode(os.urandom(16)).decode()
        self.s.sendall((f"GET /{path} HTTP/1.1\r\nHost: {hostport}\r\nUpgrade: websocket\r\n"
                        f"Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\n"
                        f"Sec-WebSocket-Version: 13\r\n\r\n").encode())
        buf = b''
        while b'\r\n\r\n' not in buf:
            buf += self.s.recv(4096)
        self.buf = buf.split(b'\r\n\r\n', 1)[1]
        self.n = 0

    def send(self, obj):
        data = json.dumps(obj).encode()
        n, mask = len(data), os.urandom(4)
        hdr = b'\x81'
        if n < 126:
            hdr += bytes([0x80 | n])
        elif n < 65536:
            hdr += bytes([0x80 | 126]) + struct.pack('>H', n)
        else:
            hdr += bytes([0x80 | 127]) + struct.pack('>Q', n)
        self.s.sendall(hdr + mask + bytes(b ^ mask[i % 4] for i, b in enumerate(data)))

    def _read(self, n):
        while len(self.buf) < n:
            chunk = self.s.recv(1 << 20)
            if not chunk:
                raise EOFError
            self.buf += chunk
        out, self.buf = self.buf[:n], self.buf[n:]
        return out

    def recv(self):
        while True:
            b1, b2 = self._read(2)
            ln = b2 & 0x7f
            if ln == 126:
                ln = struct.unpack('>H', self._read(2))[0]
            elif ln == 127:
                ln = struct.unpack('>Q', self._read(8))[0]
            payload = self._read(ln)
            if b1 & 0x0f == 1:
                return json.loads(payload)

    def cmd(self, method, params=None):
        self.n += 1
        self.send({'id': self.n, 'method': method, 'params': params or {}})
        while True:
            m = self.recv()
            if m.get('id') == self.n:
                if 'error' in m:
                    raise RuntimeError(f'{method}: {m["error"]}')
                return m.get('result', {})


def login(base, creds):
    user, password = creds.split(':', 1)
    req = urllib.request.Request(base + '/api/auth/login', data=json.dumps({'username': user, 'password': password}).encode(),
                                 headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(req) as resp:
        for h, v in resp.headers.items():
            if h.lower() == 'set-cookie' and v.startswith('nkt_session='):
                return v.split(';', 1)[0].split('=', 1)[1]
    raise SystemExit(f'{base}: нет cookie сессии после входа')


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--cdp', type=int, default=9350)
    # Хост и хаб — под разными именами (localhost и 127.0.0.1): cookie
    # сессии браузер хранит по имени, без порта, и две панели на одном
    # имени затирали бы друг другу вход.
    ap.add_argument('--host', default='http://localhost:8077')
    ap.add_argument('--host-login', required=True)
    ap.add_argument('--hub', default='http://127.0.0.1:8112')
    ap.add_argument('--hub-login', required=True)
    ap.add_argument('--out', default=os.path.join(os.path.dirname(__file__), '..', 'site', 'public', 'screens'))
    ap.add_argument('--only', help='имена экранов через запятую')
    ap.add_argument('--langs', default='ru,en')
    ap.add_argument('--width', type=int, default=1400)
    ap.add_argument('--height', type=int, default=900)
    args = ap.parse_args()

    bases = {'host': args.host, 'hub': args.hub}
    cookies = {'host': login(args.host, args.host_login), 'hub': login(args.hub, args.hub_login)}
    tabs = json.load(urllib.request.urlopen(f'http://127.0.0.1:{args.cdp}/json/list'))
    ws = WS([t for t in tabs if t['type'] == 'page'][0]['webSocketDebuggerUrl'])
    ws.cmd('Page.enable')
    ws.cmd('Network.enable')
    ws.cmd('Emulation.setDeviceMetricsOverride', {'width': args.width, 'height': args.height, 'deviceScaleFactor': 1, 'mobile': False})
    for where, base in bases.items():
        ws.cmd('Network.setCookie', {'name': 'nkt_session', 'value': cookies[where], 'url': base, 'path': '/'})

    only = set(args.only.split(',')) if args.only else None
    for lang in args.langs.split(','):
        # Язык и тема — из localStorage, как их хранит интерфейс; ставятся до
        # загрузки страницы, чтобы не ловить перерисовку.
        pre = ws.cmd('Page.addScriptToEvaluateOnNewDocument', {
            'source': f"localStorage.setItem('nkt-lang', {lang!r}); localStorage.setItem('nkt-theme', 'light'); localStorage.setItem('nkt-sidebar-collapsed', '0');"})
        outdir = os.path.join(args.out, lang)
        os.makedirs(outdir, exist_ok=True)
        for name, where, path, action in SCREENS:
            if only and name not in only:
                continue
            ws.cmd('Page.navigate', {'url': bases[where] + path})
            time.sleep(4)
            if action:
                ws.cmd('Runtime.evaluate', {'expression': '(async () => {' + (HELPERS % {'lang': lang}) + action + '})()', 'awaitPromise': True})
                time.sleep(2.5)
            ws.cmd('Runtime.evaluate', {'expression': CLEANUP})
            res = ws.cmd('Page.captureScreenshot', {'format': 'png'})
            out = os.path.join(outdir, name + '.png')
            with open(out, 'wb') as f:
                f.write(base64.b64decode(res['data']))
            print(f'{lang}/{name}.png', file=sys.stderr)
        ws.cmd('Page.removeScriptToEvaluateOnNewDocument', {'identifier': pre['identifier']})


if __name__ == '__main__':
    main()
