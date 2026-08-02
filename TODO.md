# TODO

Остаток находок аудита безопасности от 2026-08-01 (`проверь безопасность проекта`).
Эксплуатируемых уязвимостей нет — это варианты усиления defence-in-depth,
приоритет — сверху вниз. Пункты 1-2 (сессии при смене пароля, `NKT_COOKIE_SECURE`
по умолчанию) уже закрыты.

- [ ] **CSRF защищён только `SameSite=Lax`** — токена или проверки `Origin`/
      `Sec-Fetch-Site` нет (`internal/auth/service.go` — `SetSessionCookie`).
      `SameSite=Lax` закрывает форменный CSRF, но это единственный слой.

- [ ] **Нет `Content-Security-Policy` и `Strict-Transport-Security`** —
      `internal/api/server.go:233-240` (`securityHeaders`) ставит только
      `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`.

- [ ] **Троттлинг входа — по логину и в памяти** —
      `internal/auth/service.go:142-172` (`allowAttempt`/`failAttempt`).
      После 5 неудач экспоненциальный backoff, но не по IP и сбрасывается при
      рестарте: подбор логинов россыпью с одного IP не замедляется.

- [ ] **`ContainerAction` проверяет имя контейнера блок-листом** —
      `internal/control/services.go:133`:
      `strings.ContainsAny(name, "/?&#")`, тогда как везде рядом (`lineageRe`,
      `hostnameRe`, `RuleSpec.Validate`) используется allowlist-регулярка.
      Практической дыры нет (chi не пускает `/` в сегмент пути), но паттерн
      внутренне непоследователен.

- [ ] **`Which()` собирает shell-строку** —
      `internal/collect/collector.go:124-126`:
      `sh -c "command -v "+binary`. Сейчас туда попадают только зашитые
      литералы (`nginx`, `haproxy`, ...) — риска нет, но паттерн опасен, если
      когда-нибудь получит аргумент из запроса.

- [ ] **Редактор конфигов не проверяет symlink-побег** —
      `internal/control/configs.go:97` (`checkPath`). Allowlist + запрет `..`
      корректны, но пути не резолвятся через симлинки: если внутри
      разрешённого каталога уже лежит симлинк наружу, `ReadFile`/`Stat` пойдут
      за ним. Эксплуатация требует, чтобы кто-то уже имел доступ на запись в
      файловую систему — не через эти хендлеры напрямую.

- [ ] **TUI: поля сертификата не экранированы `tview.Escape()`** —
      `internal/tui/screens_certs.go`, `describe()`:
      `cert.Error` (457), `cert.Issuer`/`cert.Subject` (465-466),
      `cert.Renewal.Detail` (480). Тот же файл уже правильно экранирует
      certbot-вывод в `renderRenewLog`, и `screens_configs.go` экранирует
      содержимое конфигов — тут паттерн пропущен. Поле сертификата
      (Subject/Issuer) теоретически может содержать `[тег]`-последовательности
      tview и испортить отображение — не RCE, но искажение статуса на экране.
