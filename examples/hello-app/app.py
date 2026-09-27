"""hello-app — пример приложения для выкладок nkt.

Одна страница с версией (тегом образа) и /healthz для проверок.
Только стандартная библиотека Python: образ маленький, зависимостей нет.
"""

import json
import os
import socket
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = os.environ.get("APP_VERSION", "dev")
STARTED = datetime.now(timezone.utc)

PAGE = """<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>hello-app</title>
<style>body{{font-family:system-ui,sans-serif;margin:3rem;color:#222}}code{{background:#f3f3f3;padding:.1rem .3rem}}</style>
</head><body>
<h1>hello-app</h1>
<p>version <code>{version}</code> on <code>{host}</code></p>
<p>started {started}</p>
</body></html>
"""


def render_page() -> str:
    return PAGE.format(version=VERSION, host=socket.gethostname(), started=STARTED.isoformat(timespec="seconds"))


class Handler(BaseHTTPRequestHandler):
    server_version = "hello-app"

    def do_GET(self):  # noqa: N802 — имя метода задаёт http.server
        if self.path == "/healthz":
            self._send(200, "application/json", json.dumps({"ok": True, "version": VERSION}))
        elif self.path in ("/", "/index.html"):
            self._send(200, "text/html; charset=utf-8", render_page())
        else:
            self._send(404, "text/plain; charset=utf-8", "not found\n")

    def _send(self, code: int, ctype: str, body: str):
        data = body.encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):  # журнал — одной строкой в stdout
        print(f"{self.address_string()} {fmt % args}", flush=True)


def main():
    port = int(os.environ.get("PORT", "8000"))
    print(f"hello-app {VERSION} listening on :{port}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
