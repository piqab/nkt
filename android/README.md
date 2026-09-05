# nkt hub — Android-клиент

Нативное Android-приложение (Kotlin/Jetpack Compose), выполняющее роль
клиента к уже существующему JSON API `nkt hub` — так же, как это делает
`web/` (React-фронтенд). Хаб-сервер (VPS, держащий SSH-туннели к
управляемым хостам) не меняется: приложение лишь добавляет ещё один
способ говорить с тем же API, что и браузер.

Полный план (архитектура, разбивка на фазы, разведка по API/WebSocket/
экранам) — в репозитории Claude-плана этой сессии; при необходимости
попросите его продублировать сюда. Коротко: план разбит на 8 фаз —
от каркаса до полного паритета со всеми ~18 экранами веб-интерфейса,
коммит на каждую фазу.

## Статус: фаза 1 из 8

Реализовано:
- Gradle-проект (AGP 8.6.0, Kotlin 2.0.20, Compose, `kotlinx.serialization`).
- Сетевой слой (`net/HubClient.kt`) — один `OkHttpClient` + персистентный
  `CookieJar` (`net/PersistentCookieJar.kt`, поверх DataStore), host-scoping
  по правилу `web/src/api.ts`'s `hostScope` (`net/HostScope.kt`).
- JSON-модели (`net/model/Models.kt`), сверенные с Go-структурами.
- Экран входа (`ui/login`), список хостов только для чтения (`ui/hosts`),
  экран «О системе» (`ui/about`) — версия хаба + статус базы уязвимостей,
  без кнопок обновления (см. `AboutViewModel`'s doc-комментарий — почему).
- Заглушка экрана хоста (`MainActivity.kt`'s `HostPlaceholderScreen`) —
  доказывает, что цепочка логин → список хостов → открытие хоста
  работает целиком; реальные экраны хоста — фаза 2.

Не реализовано (следующие фазы): всё, что требует открытия конкретного
хоста (Overview, Findings, Services, Containers, Terminal, Firewall,
Configs, Certificates, Vulnerabilities, Topology, ...), управление
хостами на уровне хаба (add/edit/export/import), WebSocket-стриминг.

## Сборка из командной строки, без Android Studio

Один раз на машину — установка тулчейна (JDK 17, Gradle, Android SDK).
Ставится в `$HOME/.local`, root/sudo не нужен, ничего системного не
трогает — та же схема, что у go/node в этом репозитории:

```bash
android/scripts/setup-linux-toolchain.sh
```

Дальше сборка:

```bash
android/scripts/build.sh                  # debug APK
android/scripts/build.sh assembleRelease  # любые аргументы уходят в Gradle
```

Либо вручную, тем же путём:

```bash
cd android
source scripts/env.sh
./gradlew assembleDebug
```

Результат — `android/app/build/outputs/apk/debug/app-debug.apk`.
Установка на подключённое устройство/эмулятор:

```bash
$ANDROID_HOME/platform-tools/adb install -r \
  android/app/build/outputs/apk/debug/app-debug.apk
```

Android Studio при этом никуда не девается: `android/` — обычный
Gradle-проект, его можно открыть в IDE и собирать оттуда тем же
wrapper'ом.

### Что проверено, а что нет

Сборка (`assembleDebug`) проходит, APK собирается — это проверено.
Приложение при этом **ни разу не запускалось**: ни на эмуляторе, ни на
устройстве. То есть компиляция и упаковка подтверждены, а поведение в
рантайме (вход, реальные запросы к хабу, отрисовка экранов) — нет.
Прежде чем полагаться на код, запустите APK и пройдите вход на реальном
`nkt hub` (адрес вида `http://192.168.1.10:8080` вводится на экране
входа).

## Заметки по конфигурации

- `minSdk = 26`, `targetSdk = compileSdk = 35`.
- `network_security_config.xml` разрешает `cleartextTrafficPermitted`
  на уровне base-config — хаб по умолчанию слушает без TLS в доверенной
  сети (см. README.md корня проекта, `NKT_COOKIE_SECURE=false`); при
  выпуске в реальную эксплуатацию через недоверенную сеть используйте
  реверс-прокси с TLS перед хабом и уберите это разрешение.
