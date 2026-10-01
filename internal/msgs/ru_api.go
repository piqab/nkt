package msgs

// Сообщения API-токенов хаба и внешнего доступа — отдельным файлом.
func init() {
	for k, v := range ruAPICatalog {
		ruCatalog[k] = v
	}
}

var ruAPICatalog = map[string]string{
	"auth.tokensUnsupported": "API-токены принимает только хаб",
	"auth.tokenInvalid":      "Неверный API-токен или подпись запроса",
	"auth.tokenTooMany":      "Слишком много неверных токенов с этого адреса — подождите несколько минут",
	"auth.tokenExpired":      "Срок API-токена «%s» истёк",
	"auth.tokenIPDenied":     "API-токену запрещён адрес %s",
	"auth.tokenStale":        "Подпись запроса устарела: часы клиента и хаба расходятся больше чем на 5 минут",
	"auth.tokenReplay":       "Эта подпись уже использована: на каждый запрос — новый nonce",
	"auth.tokenBodyTooLarge": "Тело запроса с подписью — не больше 1 МБ",
	"auth.tokenRouteDenied":  "API-токену недоступен вызов %s %s",
	"auth.tokenReadOnly":     "API-токен только для чтения: действия ему недоступны",
	"auth.tokenHostDenied":   "Хост вне пределов API-токена",
	"auth.tokenDryContent":   "API-токену с пределами — сухой прогон только сохранённого конвейера (pipeline_id, без content)",
	"hub.tokenBadName":       "Имя токена — от 1 до 64 символов в одну строку",
	"hub.tokenBadRole":       "Роль токена — read или admin, а не %q",
	"hub.tokenBadExpiry":     "Срок токена — от 0 (бессрочный) до 3650 дней, а не %d",
	"hub.tokenBadHost":       "Нет хоста с номером %d",
	"hub.tokenBadGroup":      "Нет группы %q",
	"hub.tokenBadIP":         "Не адрес и не подсеть: %q",
	"hub.tokenNameTaken":     "Токен «%s» уже есть",
	"hub.tokenMissing":       "Нет токена №%d",
	"hub.jobMissing":         "Нет задания №%d",
	"auth.tokenEdgeSigned":   "Через nkt-edge — только подписанные запросы токена (X-NKT-API-*), без Bearer и cookie",
	"auth.tokenEdgeDenied":   "Токену «%s» не разрешён доступ через nkt-edge %s",
	"edge.apiPathDenied":     "Через nkt-edge доступны только вызовы API с токеном: /api/auth/me, /api/hub/…, /api/hosts/…",
	"edge.apiNoClient":       "nkt-edge не передал адрес клиента",
	"edge.missing":           "Нет edge №%d",
	"edge.badRoles":          "Неизвестные роли edge: %s",
	"edge.manualIncomplete":  "Для edge нужны адрес туннеля и токен (EDGE_TOKEN)",
	"hub.outDeployOK":        "Выкладка «%s» прошла: %s",
	"hub.outDeployFailed":    "Выкладка «%s» (%s) не прошла: %s",
	"hub.outTestText":        "Пробное событие исходящего вебхука nkt (отправил %s)",
	"hub.outBadName":         "Имя адресата — от 1 до 64 символов в одну строку",
	"hub.outBadURL":          "Адрес — http:// или https:// без логина и пароля, а не %q",
	"hub.outBadKind":         "Неизвестный вид события: %q",
	"hub.outMissing":         "Нет исходящего вебхука №%d",
	"hub.outTooMany":         "Исходящих вебхуков — не больше 32",
}
