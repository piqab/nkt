# Разработка

Руководство разработчика переехало на сайт —
[piqab.github.io/nkt/guide/development](https://piqab.github.io/nkt/guide/development)
([English](https://piqab.github.io/nkt/en/guide/development)).

| Раздел | Ссылка |
|---|---|
| Что нужно, команды сборки (`make build`, `make native-build`, `make edge`…) | [Команды сборки](https://piqab.github.io/nkt/guide/development#команды-сборки) |
| Релизы, беты, `WHATSNEW.md` | [Релизы](https://piqab.github.io/nkt/guide/development#релизы) |
| Проверочный стенд (`make stand`) | [Проверочный стенд](https://piqab.github.io/nkt/guide/development#проверочныи-стенд) |
| Фронтенд с горячей перезагрузкой | [Фронтенд](https://piqab.github.io/nkt/guide/development#фронтенд-с-горячеи-перезагрузкои) |
| Тесты, живые тесты `NKT_TEST_LIVE_*`, пример `hello-app` | [Тесты](https://piqab.github.io/nkt/guide/development#тесты) |
| govulncheck, gosec, trivy | [Проверки безопасности](https://piqab.github.io/nkt/guide/development#проверки-безопасности) |
| Устройство пакетов, новый парсер | [Устройство](https://piqab.github.io/nkt/guide/development#устроиство) |
| Сообщения `internal/msgs` и переводы | [Сообщения и переводы](https://piqab.github.io/nkt/guide/development#сообщения-и-переводы) |
| Сайт и скриншоты | [Сайт и скриншоты](https://piqab.github.io/nkt/guide/development#саит-и-скриншоты) |

Коротко:

```bash
make build        # dist/nkt
make check        # go vet, gofmt, go test ./..., проверка типов фронтенда
make stand        # nginx, haproxy, docker и nkt на 127.0.0.1:8077
cd site && npm ci && npm run build   # сайт
```

Перед `go test ./...` остановите локальный nkt на `127.0.0.1:8077` —
тесты хаба поднимают свои процессы.

Исходник страницы — [`site/guide/development.md`](site/guide/development.md).
