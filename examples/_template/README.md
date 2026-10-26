# Заготовка плагина

Рабочий плагин-каркас для своего плагина хаба nkt: к условному REST API
«элементов» — раздел со списком, форма «Добавить», удаление с
подтверждением, сводка, команда сценария `example …`, инструменты MCP и
тест на поддельном API. В каталог не входит (имя папки начинается с `_`).

Руководство — [examples/AGENTS.md](../AGENTS.md): правила, схема манифеста,
чек-лист. Коротко:

1. Скопируйте папку: `cp -r examples/_template examples/<имя>`.
2. Замените имя (`example` → своё) в `deploy/nkt-plugin.json` и
   `deploy/nkt-plugin.py` (манифест, `log`, `server_version`), порт —
   свободный среди плагинов каталога (`grep '"port"' examples/*/deploy/nkt-plugin.json`).
3. Опишите настройки в `nkt-plugin.json` (секреты — `"secret": true`).
4. Замените `api()` и виды на настоящий API, тексты — в `T` на двух языках.
5. Перепишите подделку в `deploy/test_plugin.py` по документации API.
6. `python3 -I deploy/test_plugin.py` и
   `go run ./cmd/nkt-catalog lint -dir examples/<имя>` — без замечаний.
7. README.md и README.en.md, скриншоты — `screens/ru/`, `screens/en/`.

Настройки заготовки: `EXAMPLE_URL` (адрес API), `EXAMPLE_TOKEN` (секрет).
