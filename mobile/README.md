# nkt mobile — Android и iOS

Клиент хаба NetKnownsThat для телефона: тот же JSON API, что у веб-интерфейса,
экраны под палец. Kotlin Multiplatform: один код для Android и iOS, интерфейс
на Compose Multiplatform.

## Архитектура

Clean Architecture, зависимости направлены только внутрь:
`ui → domain ← data`.

```
shared/core       корутины, kotlinx-datetime (общие зависимости)
shared/domain     модели, интерфейсы репозиториев, use cases — чистый Kotlin
shared/data       Ktor (REST и WebSocket), DTO и преобразователи в domain,
                  реализации репозиториев, DataStore, cookie, закрепление
                  сертификата хаба (OkHttp на Android, Darwin на iOS)
shared/terminal   эмулятор VT100 — чистая логика, покрыт тестами
shared/ui         экраны (Compose), ViewModel, навигация с хлебными
                  крошками, тема, переводы t(ru, en)
shared/app        корень композиции: граф Koin, вход для iOS (Shared.framework)
androidApp        приложение Android: Activity, уведомления (WorkManager)
iosApp            приложение iOS: SwiftUI-оболочка, фоновое обновление;
                  проект Xcode генерируется XcodeGen из project.yml
```

- **domain** не знает ни HTTP, ни платформ: модели без аннотаций
  сериализации, ошибки — `AppError`, результат — `Outcome`. Хост адресуется
  явно (`HostTarget`) в каждом вызове — глобального «текущего хоста» нет.
- **data** держит DTO отдельно от моделей domain (`data/dto`,
  `data/mapper`); тела запросов собираются здесь же.
- **ui** видит только domain: ViewModel получают репозитории и use cases
  через Koin (`ui/di/PresentationModule.kt`), для хоста — с параметром
  `HostTarget`. ViewModel живёт столько же, сколько запись в стеке
  навигации.
- **DI** — Koin: `data/di/DataModule.kt` (реализации), `ui/di` (ViewModel и
  use cases), `app` собирает всё вместе с платформенными сервисами.
- **Навигация** — Navigation Compose с типизированными маршрутами
  (`ui/navigation/Routes.kt`); хлебные крошки — сам стек переходов, «Назад»
  возвращает на шаг.

## Сборка

```sh
mobile/scripts/setup-toolchain.sh      # один раз: JDK 17 и Android SDK в ~/.local
mobile/scripts/build.sh                # APK: androidApp/build/outputs/apk/debug/
mobile/scripts/build.sh testDebugUnitTest
```

iOS — только на macOS с Xcode:

```sh
brew install xcodegen
cd mobile/iosApp && xcodegen generate && open iosApp.xcodeproj
```

Сборка Xcode сама вызывает Gradle (`:shared:app:embedAndSignAppleFrameworkForXcode`).
В CI (`.github/workflows/mobile.yml`) iOS собирается под симулятор без
подписи: публикация в App Store/TestFlight требует аккаунта Apple Developer.

## Тесты

- `shared/data` — разбор настоящих ответов сервера (`api/*.json`, снятые с
  `nkt`), преобразователи, репозитории на подменном сервере (Ktor MockEngine):
  пути, язык, ошибки, cookie.
- `shared/domain` — use cases на фейковых репозиториях.
- `shared/terminal` — эмулятор на выводе bash и tmux.
- `shared/ui` — статусы, проверка блокировки доступа в firewall и
  `I18nCoverageTest`: любой русский литерал вне `t(ru, en)` валит сборку.

## Версия

`versionName` — файл `VERSION` репозитория (плюс `-beta` при
`-PnktVersionSuffix=-beta`), `versionCode` выводится из него. Подписанный APK
собирают `release.yml` (задание `android-apk`) и `android-release.yml` из
секретов `ANDROID_KEYSTORE_BASE64` и `ANDROID_KEYSTORE_PASSWORD`.
