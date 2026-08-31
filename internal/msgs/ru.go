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
	"pkgInstall.unknownService":            "неизвестный сервис: %s",
	"pkgInstall.serviceAlreadyInstalled":   "%s уже установлен",
	"pkgInstall.unknownPackage":            "неизвестный пакет: %s",
	"pkgInstall.noPackagesSelected":        "не выбрано ни одного пакета",

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

	"parse.configUnavailable":       "конфиг %s недоступен: %v",
	"parse.configParseFailed":       "разбор %s: %v",
	"parse.nginxMainConfigNotFound": "основной конфиг %s не найден",
	"parse.includeFileNotFound":     "%s: include %s — файл не найден",
	"parse.ipAddrParseFailed":       "разбор вывода ip addr: %v",
	"parse.commandFailed":           "%s завершился с кодом %d: %s",

	"parse.libvirtUnavailable":        "libvirt недоступен: %s",
	"parse.libvirtListFailed":         "libvirt: virsh list вернул код %d: %s",
	"parse.libvirtDominfoUnavailable": "libvirt: dominfo %s недоступен",
	"parse.libvirtDumpxmlUnavailable": "libvirt: dumpxml %s недоступен",
	"parse.libvirtXMLParseFailed":     "libvirt: разбор XML домена %s: %v",
	"parse.lxdUnavailable":            "lxd недоступен: %s",
	"parse.lxdListFailed":             "lxd: lxc list вернул код %d: %s",
	"parse.lxdListParseFailed":        "lxd: разбор списка: %v",
	"parse.podmanUnavailable":         "podman недоступен: %s",
	"parse.podmanListFailed":          "podman: список контейнеров вернул HTTP %d",
	"parse.podmanListParseFailed":     "podman: разбор списка контейнеров: %v",
	"parse.dockerUnavailable":         "docker недоступен: нет ни сокета движка, ни compose-файлов",
	"parse.noFirewallBackendReadable": "не удалось прочитать ни iptables, ни ufw, ни firewalld (нужны права root)",
	"parse.psNoParsedLines":           "ps отработал, но ни одной строки разобрать не удалось — возможно, у него другой формат вывода (busybox?)",
	"parse.noCgroupsRead":             "ни один /proc/<pid>/cgroup не прочитан — происхождение процессов (сервис/контейнер/вручную) определить нельзя",
	"parse.memTotalNotFound":          "/proc/meminfo: строка MemTotal не найдена",
	"parse.systemctlUnavailable":      "systemctl недоступен: состояние сервисов прочитать не удалось",

	"parse.certDirUnavailable":  "каталог сертификатов недоступен: %v",
	"parse.certDirEmpty":        "каталог сертификатов пуст",
	"parse.certFileUnavailable": "файл недоступен: %v",
	"parse.certParseFailed":     "разбор сертификата: %v",
	"parse.noPEMCertsInFile":    "в файле нет ни одного сертификата в формате PEM",

	"parse.renewalManualOutsideLE":   "путь вне /etc/letsencrypt — обновление, скорее всего, ручное",
	"parse.renewalConfMissing":       "сертификат лежит в /etc/letsencrypt/live/%s, но файла обновления /etc/letsencrypt/renewal/%s.conf нет — certbot его не продлит",
	"parse.renewalTimerActive":       "автообновление включено: таймер %s активен",
	"parse.renewalCronActive":        "автообновление включено: задание %s",
	"parse.renewalNoAutomationFound": "certbot знает о сертификате, но ни таймер certbot.timer, ни задание cron не найдены — продление не запустится само",

	"finding.portConflict.title":      "Конфликт порта %d между %s и %s",
	"finding.portConflict.detail":     "%s (%s, %s:%d) и %s (%s, %s:%d) объявляют один и тот же порт. Второй сервис не сможет занять сокет и будет падать при старте.",
	"finding.portConflict.suggestion": "Разведите сервисы по разным портам или адресам привязки.",

	"finding.declaredNotListening.title":      "Порт %d объявлен в конфиге, но никто его не слушает",
	"finding.declaredNotListening.detail":     "%s (%s) объявляет %s, но в выводе ss такого слушателя нет. Либо конфиг не применён (нужен reload), либо сервис не смог занять порт.",
	"finding.declaredNotListening.suggestion": "Проверьте, применён ли конфиг: перезагрузите %s и посмотрите журнал.",

	"finding.listeningNotDeclared.title":        "Неучтённый слушатель на порту %d (%s)",
	"finding.listeningNotDeclared.detail":       "Процесс %s слушает %s:%d, но этот порт не описан ни в одном из разобранных конфигов.",
	"finding.listeningNotDeclared.detailPublic": "Процесс %s слушает %s:%d, но этот порт не описан ни в одном из разобранных конфигов. Сокет открыт на всех интерфейсах.",
	"finding.listeningNotDeclared.suggestion":   "Убедитесь, что сервис нужен, и опишите его в конфигурации или закройте порт.",

	"finding.noDefaultDeny.title":      "Политика INPUT по умолчанию — ACCEPT, менеджер firewall не активен",
	"finding.noDefaultDeny.detail":     "Входящий трафик разрешён по умолчанию: любой открытый порт доступен извне вне зависимости от того, планировали вы это или нет.",
	"finding.noDefaultDeny.suggestion": "Включите ufw (ufw default deny incoming) или firewalld, либо задайте iptables -P INPUT DROP и явно разрешите нужные порты.",

	"finding.publicPortBlocked.title":      "Порт %d открыт сервисом, но закрыт firewall",
	"finding.publicPortBlocked.detail":     "%s (%s) слушает %s на всех интерфейсах, но правил, разрешающих входящий трафик на этот порт, нет, а политика INPUT — %s. Снаружи сервис недоступен.",
	"finding.publicPortBlocked.suggestion": "Если сервис должен быть доступен: ufw allow %d/tcp.",

	"finding.dockerBypassesFirewall.title":           "Контейнер %s публикует порт %d на 0.0.0.0 в обход firewall",
	"finding.dockerBypassesFirewall.detail":          "Docker добавляет правила DNAT в цепочку PREROUTING/FORWARD, поэтому опубликованный порт %d не проходит через INPUT и не закрывается правилами ufw.",
	"finding.dockerBypassesFirewall.detailSensitive": "Docker добавляет правила DNAT в цепочку PREROUTING/FORWARD, поэтому опубликованный порт %d не проходит через INPUT и не закрывается правилами ufw. На этом порту работает %s — публиковать его наружу почти наверняка не нужно.",
	"finding.dockerBypassesFirewall.suggestion":      "Привяжите публикацию к localhost (\"127.0.0.1:%d:%d\") или используйте цепочку DOCKER-USER для фильтрации.",

	"finding.staleFirewallRule.title":             "Правило firewall для порта %d не используется",
	"finding.staleFirewallRule.detail":            "Правило разрешает входящий трафик на порт %d, но на хосте нет процесса, который его слушает.",
	"finding.staleFirewallRule.detailZeroPackets": "Правило разрешает входящий трафик на порт %d, но на хосте нет процесса, который его слушает. Счётчик правила равен нулю — трафика по нему не было.",
	"finding.staleFirewallRule.suggestion":        "Удалите правило, если сервис больше не нужен: ufw delete allow %d/tcp.",

	"finding.sensitivePortPublic.title":           "%s слушает порт %d на всех интерфейсах",
	"finding.sensitivePortPublic.detail":          "Процесс %s принимает подключения на 0.0.0.0:%d. Такие сервисы обычно не рассчитаны на публичный доступ и не имеют собственной защиты от перебора.",
	"finding.sensitivePortPublic.detailReachable": "Процесс %s принимает подключения на 0.0.0.0:%d. Порт при этом не закрыт правилами firewall. Такие сервисы обычно не рассчитаны на публичный доступ и не имеют собственной защиты от перебора.",
	"finding.sensitivePortPublic.suggestion":      "Привяжите сервис к 127.0.0.1 или к внутренней сети и закройте порт на firewall.",

	"finding.weakTLS.title":      "Включены устаревшие версии TLS: %s",
	"finding.weakTLS.detail":     "ssl_protocols = %q. Эти версии считаются небезопасными и отключены в современных браузерах.",
	"finding.weakTLS.suggestion": "Оставьте только TLSv1.2 и TLSv1.3: ssl_protocols TLSv1.2 TLSv1.3;",

	"finding.missingHSTS.title":      "Нет заголовка HSTS на %s",
	"finding.missingHSTS.detail":     "TLS-сервер не отдаёт Strict-Transport-Security, поэтому клиент может быть возвращён на http.",
	"finding.missingHSTS.suggestion": `add_header Strict-Transport-Security "max-age=31536000" always;`,

	"finding.tlsCertMissing.title":      "listen ... ssl без ssl_certificate на %s",
	"finding.tlsCertMissing.detail":     "Слушатель объявлен как TLS, но сертификат в блоке не задан — nginx не запустится.",
	"finding.tlsCertMissing.suggestion": "Добавьте ssl_certificate и ssl_certificate_key — быстрый способ получить файлы: сгенерировать самоподписанный сертификат на странице «Сертификаты».",

	"finding.tlsCertUnreadable.title":      "Сертификат не читается: %s",
	"finding.tlsCertUnreadable.detail":     "%s указан в конфигурации для %s, но прочитать его не удалось: %s. Если файла действительно нет, сервис не поднимет TLS-слушатель.",
	"finding.tlsCertUnreadable.suggestion": "Проверьте путь и права доступа, при необходимости выпустите сертификат заново.",

	"finding.tlsCertExpired.title":  "Сертификат %s просрочен на %d дн.",
	"finding.tlsCertExpired.detail": "Срок действия истёк %s. Обслуживает %s. Браузеры показывают ошибку и не пускают пользователей дальше.",

	"finding.tlsCertExpiring.title":          "Сертификат %s истекает через %d дн.",
	"finding.tlsCertExpiring.detail":         "Действителен до %s, обслуживает %s. %s",
	"finding.tlsCertExpiring.detailWithTime": "Действителен до %s, обслуживает %s. %s",

	"finding.tlsCertNotYetValid.title":      "Сертификат ещё не вступил в силу: %s",
	"finding.tlsCertNotYetValid.detail":     "Действителен только с %s. Обычно это значит, что часы на хосте отстают или сертификат выпущен «на будущее».",
	"finding.tlsCertNotYetValid.suggestion": "Проверьте системное время и дату выпуска сертификата.",

	"finding.tlsCertRenewalNotAutomatic.title":      "Автообновление сертификата не запускается: %s",
	"finding.tlsCertRenewalNotAutomatic.suggestion": "Включите таймер: systemctl enable --now certbot.timer — либо добавьте задание cron.",

	"finding.tlsCertOrphanLineage.title":      "Сертификат certbot остался без файла обновления",
	"finding.tlsCertOrphanLineage.suggestion": "Выпустите сертификат заново через certbot certonly, чтобы восстановить запись обновления.",

	"finding.tlsCertNotReloaded.title":      "На сокете отдаётся другой сертификат, чем указан в конфиге",
	"finding.tlsCertNotReloaded.detail":     "Файл %s не совпадает с тем, что реально отдаёт %s при TLS-подключении: на сокете сертификат с серийным номером %s, действителен до %s. Обычно это значит, что файл на диске обновили (например, certbot renew), а сервис не перечитал конфигурацию.",
	"finding.tlsCertNotReloaded.suggestion": "Перезагрузите %s, чтобы он подхватил актуальный сертификат.",

	"finding.tlsCertSelfSigned.title":      "Самоподписанный сертификат на %s",
	"finding.tlsCertSelfSigned.detail":     "Издатель совпадает с субъектом (%s). Такому сертификату не доверяет ни один браузер; для внутренних сервисов это допустимо, для публичных — нет.",
	"finding.tlsCertSelfSigned.suggestion": "Для публичного сервиса выпустите сертификат в доверенном центре.",

	"finding.tlsCertWeakKey.title":      "Слабый ключ RSA %d бит",
	"finding.tlsCertWeakKey.detail":     "Сертификат %s использует ключ короче %d бит. Современные клиенты такие соединения отклоняют.",
	"finding.tlsCertWeakKey.suggestion": "Перевыпустите сертификат с ключом RSA 2048+ или ECDSA P-256.",

	"finding.tlsCertWeakSignature.title":      "Устаревший алгоритм подписи: %s",
	"finding.tlsCertWeakSignature.detail":     "Подписи на основе SHA-1 и MD5 считаются небезопасными и не принимаются современными браузерами.",
	"finding.tlsCertWeakSignature.suggestion": "Перевыпустите сертификат с подписью SHA-256 или сильнее.",

	"finding.tlsCertNameMismatch.title":      "Сертификат не покрывает имя %s",
	"finding.tlsCertNameMismatch.detail":     "Сервер отвечает на %s, но сертификат %s выписан на %s. Клиент увидит предупреждение о несоответствии имени.",
	"finding.tlsCertNameMismatch.suggestion": "Добавьте %s в SAN сертификата или используйте отдельный сертификат.",

	"finding.renewalSuggestionCertbot": "Продлите сейчас: certbot renew --cert-name %s, затем перезагрузите сервис.",
	"finding.renewalSuggestionManual":  "Выпустите и установите новый сертификат, затем перезагрузите сервис.",

	"finding.publicPlaintextProxy.title":      "%s проксирует трафик по HTTP без TLS",
	"finding.publicPlaintextProxy.detail":     "Слушатель %s принимает запросы на всех интерфейсах без шифрования и передаёт их дальше. Заголовки, cookie и токены идут открытым текстом.",
	"finding.publicPlaintextProxy.suggestion": "Переведите сервис на https (для быстрого теста можно сгенерировать самоподписанный сертификат на странице «Сертификаты») или оставьте на 80 только редирект на https.",

	"finding.upstreamUndefined.title":      "Ссылка на несуществующий upstream %q",
	"finding.upstreamUndefined.detail":     "Маршрут %q в %s указывает на пул %q, но такой пул не определён.",
	"finding.upstreamUndefined.suggestion": "Проверьте имя пула или добавьте соответствующий блок upstream/backend.",

	"finding.upstreamOrphan.title":      "Пул %q объявлен, но нигде не используется",
	"finding.upstreamOrphan.detail":     "Ни один маршрут не ссылается на этот пул — вероятно, остаток от прошлой конфигурации.",
	"finding.upstreamOrphan.suggestion": "Удалите неиспользуемый блок или подключите его к нужному маршруту.",

	"finding.upstreamMemberDown.title":      "Backend %s пула %q не слушает порт",
	"finding.upstreamMemberDown.detail":     "Пул %q отправляет трафик на %s, но локально этот порт никем не занят. Запросы будут завершаться ошибкой 502/504.",
	"finding.upstreamMemberDown.suggestion": "Поднимите сервис на этом порту или уберите его из пула.",

	"finding.singleBackend.title":      "В пуле %q один сервер — нет резерва",
	"finding.singleBackend.detail":     "Отказ единственного backend приведёт к полной недоступности маршрута.",
	"finding.singleBackend.suggestion": "Добавьте второй сервер или явный backup.",

	"finding.allBackendsDisabled.title":      "Все серверы пула %q помечены down/backup",
	"finding.allBackendsDisabled.detail":     "Активных серверов не осталось — весь трафик на этот пул будет отвергнут.",
	"finding.allBackendsDisabled.suggestion": "Верните в строй хотя бы один сервер.",

	"finding.backendNoHealthcheck.title":             "В пуле %q нет проверки здоровья серверов",
	"finding.backendNoHealthcheck.detail":            "Из %d серверов проверку имеют %d. Балансировщик будет продолжать отправлять запросы на упавший backend.",
	"finding.backendNoHealthcheck.suggestionHAProxy": "Добавьте параметр check к каждой строке server и option httpchk в backend.",
	"finding.backendNoHealthcheck.suggestionNginx":   "Задайте max_fails и fail_timeout для пассивной проверки.",
	"finding.backendNoHealthcheck.suggestionCaddy":   "Добавьте health_uri/health_interval в reverse_proxy для активной проверки.",

	"finding.containerRestarting.title":      "Контейнер %s в цикле перезапуска",
	"finding.containerRestarting.detail":     "Статус: %s. Обычно это конфликт порта, ошибка конфигурации или падение процесса на старте.",
	"finding.containerRestarting.suggestion": "Посмотрите журнал: docker logs %s.",

	"finding.containerNotRunning.title":      "Контейнер %s описан в compose, но не запущен",
	"finding.containerNotRunning.detail":     "Сервис %q из файла %s отсутствует среди работающих контейнеров.",
	"finding.containerNotRunning.suggestion": "Запустите стек: docker compose up -d.",

	"finding.containerUndeclared.title":      "Контейнер %s запущен вне compose-файлов",
	"finding.containerUndeclared.detail":     "Контейнер работает, но не описан ни в одном из известных compose-файлов — его состояние не воспроизводимо.",
	"finding.containerUndeclared.suggestion": "Опишите контейнер в compose или добавьте его файл в NKT_COMPOSE_FILES.",

	"finding.containerNoRestartPolicy.title":      "У контейнера %s не задана политика перезапуска",
	"finding.containerNoRestartPolicy.detail":     "После перезагрузки хоста или падения процесса контейнер не поднимется сам.",
	"finding.containerNoRestartPolicy.suggestion": "Добавьте restart: unless-stopped.",

	"finding.adminInterfaceOpen.title":      "Панель статистики %s доступна без пароля",
	"finding.adminInterfaceOpen.detail":     "Секция %q включает stats и слушает %s, но директивы stats auth нет. Любой, кто дотянется до порта, увидит состав backend-ов и состояние сервисов.",
	"finding.adminInterfaceOpen.suggestion": "Добавьте stats auth <user>:<password> и привяжите bind к внутреннему адресу.",
}
