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

	"certgen.lineageRequired": "укажите lineage",

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
}
