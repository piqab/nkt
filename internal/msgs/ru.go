package msgs

// ruCatalog is the default/fallback language — see DefaultLang. Every key
// here mirrors the exact text that already shipped before this package
// existed, so converting a call site to msgs.T never changes Russian
// behavior, only adds an English counterpart.
var ruCatalog = map[string]string{
	"err.notFound":       "not found",
	"err.fileNotFound":   "файл не найден",
	"err.pathNotAllowed": "файл вне разрешённых каталогов",
	"err.tooLarge":       "файл слишком большой для редактора",

	"auth.usernamePasswordRequired": "Укажите логин и пароль",
	"auth.minPasswordLength":        "Пароль должен быть не короче 10 символов",
	"auth.loginAndPasswordRequired": "Нужен логин и пароль должен быть не короче 10 символов",
	"auth.cannotRemoveOwnAdmin":     "Нельзя снять с себя роль admin",
	"auth.cannotDisableOwnAccount":  "Нельзя отключить собственную учётную запись",
	"auth.cannotDeleteOwnAccount":   "Нельзя удалить собственную учётную запись",
	"auth.createUserFailed":         "Не удалось создать пользователя: %s",
	"auth.loginRequired":            "Требуется вход в систему",

	"certgen.lineageRequired":       "укажите lineage",
	"certgen.stoppingForStandalone": "Останавливаю nginx и haproxy для --standalone…",
	"certgen.serviceStopped":        "%s: остановлен",
	"certgen.serviceStarted":        "%s: запущен",
	"certgen.errorPrefix":           "Ошибка: %s",
	"certgen.running":               "Запускаю: %s",
	"certgen.certRenewed":           "certbot: сертификат продлён",
	"certgen.certIssued":            "certbot: сертификат выпущен для %s",
	"certgen.recombinedFile":        "Пересобран файл для %s: %s",
	"certgen.checkingPort":          "Проверяю порт %d…",
	"certgen.startingRenewal":       "Начинаю продление %s",
	"certgen.startingIssuance":      "Начинаю выпуск сертификата для %s",

	"job.notFound": "Задача не найдена",

	"monitor.invalidTargetId":     "Некорректный идентификатор цели",
	"monitor.schedulerNotRunning": "Планировщик не запущен",
	"monitor.nothingToChange":     "Нечего менять",
	"monitor.invalidRuleNumber":   "Некорректный номер правила",

	"pkgInstall.fixturesDisabled":          "установка пакетов недоступна в режиме fixtures",
	"pkgInstall.aptGetMissing":             "apt-get не найден — установка пакетов поддерживается только на Debian/Ubuntu",
	"pkgInstall.dbusAlreadyAvailable":      "D-Bus уже доступен",
	"pkgInstall.dbusManualOnly":            "автоматическая установка недоступна (нужен CAP_SYS_ADMIN в systemd-юните и nsenter на хосте) — установите dbus вручную",
	"pkgInstall.tmuxAlreadyInstalled":      "tmux уже установлен",
	"pkgInstall.ufwAlreadyInstalled":       "ufw уже установлен",
	"pkgInstall.firewalldAlreadyInstalled": "firewalld уже установлен",

	"pkgUpdate.fixturesDisabled": "обновление пакетов недоступно в режиме fixtures",
	"pkgUpdate.aptGetMissing":    "apt-get не найден — обновление пакетов поддерживается только на Debian/Ubuntu",

	"terminal.disabled":         "веб-терминал выключен: задайте NKT_TERMINAL_ENABLED=true",
	"terminal.fixturesDisabled": "веб-терминал недоступен в режиме fixtures",
	"terminal.tmuxStartFailed":  "не удалось запустить tmux: %s",

	"vulns.scanAlreadyRunning": "сканирование уже выполняется",

	"configs.staleContent":         "Файл изменился с момента открытия в редакторе. Перечитайте его и повторите правку.",
	"configs.invalidVersionNumber": "Некорректный номер версии",
	"configs.validationFailed":     "Конфигурация не прошла проверку, изменения отменены.",
	"configs.fileSaved":            "Файл сохранён.",
	"configs.fileSavedAndReloaded": "Файл сохранён и конфигурация перезагружена.",
	"configs.applyFailed":          " Применить не удалось: %s",
	"configs.versionRestored":      "Восстановлена версия #%d.",

	"server.unknownApiMethod": "Неизвестный метод API: %s",
	"server.frontendNotBuilt": "Фронтенд не собран: запустите npm run build в каталоге web/",

	"selfupdate.localModeOnly":      "самообновление доступно только в режиме local",
	"selfupdate.parseRequestFailed": "разбор запроса: %s",
	"selfupdate.missingBinaryFile":  "нет файла binary: %s",
	"selfupdate.incompleteRequest":  "запрос неполный: нужны unit, env и sha256",
	"selfupdate.stageDirFailed":     "каталог для обновления: %s",
	"selfupdate.writeBinaryFailed":  "запись бинарника: %s",
	"selfupdate.checksumMismatch":   "контрольная сумма бинарника не совпала (получено %s, ожидалось %s) — передача повреждена, попробуйте ещё раз",
	"selfupdate.writeUnitFailed":    "запись systemd-юнита: %s",
	"selfupdate.writeEnvFailed":     "запись nkt.env: %s",
	"selfupdate.startFailed":        "запуск обновления: %s",

	"hub.noInstallsYet":          "для этого хоста ещё не было установок",
	"hub.localScannerNotRunning": "локальный сканер хаба не запущен",
	"hub.hostUnreachable":        "хост недоступен: %s",

	"hub.startingInstall":        "Начинаю установку",
	"hub.installCancelledByUser": "Установка отменена пользователем",
	"hub.connectingSSH":          "Подключаюсь по SSH к %s…",
	"hub.hostArch":               "Хост: %s/%s",
	"hub.waitingHealth":          "Жду, пока сервис ответит на /health…",
	"hub.checkingAdminAccount":   "Проверяю учётную запись администратора…",
	"hub.loginFailedResetting": "Вход не удался — на хосте уже есть учётная запись администратора с другим " +
		"паролем (например, от прошлой попытки установки); сбрасываю пароль на хосте…",
	"hub.adminPasswordSynced": "Пароль администратора на хосте синхронизирован",
	"hub.done":                "Готово",

	"hub.goNotWorkingInstalling": "go (%s) недоступен — устанавливаю собственный Go для хаба…",
	"hub.usingCachedGo":          "Использую ранее установленный Go хаба: %s",
	"hub.downloadingGo":          "Скачиваю %s для linux-%s…",
	"hub.goInstalled":            "Go установлен: %s",

	"hub.sourceNotFoundUsingBinDir": "Исходники не найдены в %s — использую %s (каталог бинарника хаба)",
	"hub.usingCachedBinary":         "Использую уже собранный бинарник для %s/%s",
	"hub.buildingBinary":            "Собираю бинарник для %s/%s…",
	"hub.uploadingUnitAndConfig":    "Заливаю systemd-юнит и конфигурацию…",
	"hub.installingFiles":           "Устанавливаю файлы…",
	"hub.startingSystemdService":    "Запускаю systemd-сервис…",
	"hub.uploadingBinary":           "Заливаю бинарник… %d%% (%.1f МБ из %.1f МБ)",

	"hub.sendingViaTunnel":          "Отправляю бинарник и конфигурацию через резервный канал…",
	"hub.hostAcceptedRestarting":    "Хост принял обновление и перезапускает сервис…",
	"hub.sshUnavailableUsingTunnel": "SSH недоступен — обновляю через резервный канал (%s/%s)…",
	"hub.retryingTunnelUpdate":      "Повторяю отправку обновления через резервный канал…",
	"hub.doneViaTunnel":             "Готово (через резервный канал)",
}
