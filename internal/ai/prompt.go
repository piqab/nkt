package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Промпты: что именно спрашивается у модели.
//
// Формулировки здесь важнее, чем кажется. Модель по умолчанию любит
// давать общие советы («настройте firewall», «обновите систему») — они
// бесполезны тому, кто уже смотрит на конкретную находку. Поэтому в
// инструкции прямо сказано: опираться на приведённый контекст, называть
// конкретные файлы и команды этого хоста, не выдумывать того, чего в
// контексте нет, и честно писать «данных недостаточно».

// Kind — что разбираем: от этого зависит и инструкция, и ключ кэша.
const (
	KindFinding  = "finding"
	KindVuln     = "vuln"
	KindMalware  = "malware"
	KindEvent    = "event"
	KindJobError = "job-error"
	// KindConfigError — правка конфигурации не прошла проверку или apply:
	// модели даются вывод проверки и дифф правки.
	KindConfigError = "config-error"
	// KindConfig — помощь по конфигурации: что настроено в файле, что
	// поправить, пример под задачу.
	KindConfig    = "config"
	KindMap       = "map"
	KindHubReview = "hub-review"
	// KindIP — проверка внешнего адреса из оповещения: кто это, чем
	// опасен, стоит ли банить на всех хостах. Контекст собирает хаб:
	// баны на хостах, события журналов fail2ban, обратный DNS.
	KindIP = "ip"
	// KindMonitoring — «Мониторинг» хаба: прогнозы и подсказки по
	// нагрузке и доступности всех хостов — что делать в первую очередь.
	KindMonitoring = "monitoring"
)

// systemPrompt — общая инструкция для разбора одной находки.
func systemPrompt(lang msgs.Lang) string {
	ru := lang != msgs.EN
	if ru {
		return strings.Join([]string{
			"Ты помогаешь системному администратору разобраться с находкой на его Linux-сервере.",
			"Отвечай по-русски, коротко и по делу, без вступлений и без общих слов.",
			"Опирайся только на приведённый контекст: не придумывай сервисы, файлы и настройки, которых в нём нет.",
			"Если данных не хватает для вывода — так и скажи и назови, что посмотреть.",
			"Структура ответа — ровно три раздела, каждый начинается со строки «## »:",
			"## Что это значит — 2–4 предложения простым языком.",
			"## Чем это грозит здесь — опираясь на контекст: что именно может случиться на этом хосте.",
			"## Что сделать — пронумерованные шаги; команды — отдельными строками в блоке ```bash```.",
			"Команды пиши так, чтобы их можно было выполнить на этом хосте как есть.",
			"Не предлагай выключать защиту ради удобства и не советуй действий, которые уронят доступ к серверу, без явного предупреждения.",
		}, "\n")
	}
	return strings.Join([]string{
		"You help a system administrator deal with a finding on their Linux server.",
		"Answer in English, briefly and to the point, with no preamble and no generic advice.",
		"Rely only on the given context: do not invent services, files or settings that are not in it.",
		"If the context is not enough for a conclusion, say so and name what to look at.",
		"Structure: exactly three sections, each starting with a '## ' line:",
		"## What this means — 2-4 plain sentences.",
		"## Why it matters here — based on the context: what can actually happen on this host.",
		"## What to do — numbered steps; commands on their own lines in a ```bash``` block.",
		"Write commands so they can be run on this host as is.",
		"Never suggest disabling protection for convenience, and never suggest actions that would cut off access to the server without an explicit warning.",
	}, "\n")
}

// mapSystemPrompt — инструкция архитектурного разбора карты ресурсов.
func mapSystemPrompt(lang msgs.Lang) string {
	if lang != msgs.EN {
		return strings.Join([]string{
			"Ты архитектурный ревьюер. На вход — карта ресурсов сервера (или нескольких): что слушает порты, какие сервисы и контейнеры есть, куда они ходят, какие домены и сертификаты, правила firewall, найденные проблемы.",
			"Отвечай по-русски. Не пересказывай карту — читающий её и так видит. Говори о том, чего в ней не хватает или что в ней противоречит само себе.",
			"Опирайся только на приведённые данные; если чего-то не видно — скажи об этом, а не предполагай.",
			"Структура ответа — разделы со строк «## »:",
			"## Точки отказа — что развалится от одного сбоя: единственный экземпляр, общая база, один диск.",
			"## Лишняя публичность — что доступно снаружи без надобности, где нет TLS, где админка открыта всем.",
			"## Несогласованности — апстрим в никуда, сертификат не на тот домен, правило firewall против опубликованного порта.",
			"## С чего начать — 3–5 пунктов в порядке важности, каждый с названием конкретного узла из карты.",
			"Если по какому-то разделу замечаний нет — напиши «замечаний нет», не выдумывай.",
		}, "\n")
	}
	return strings.Join([]string{
		"You are an architecture reviewer. The input is a resource map of a server (or several): what listens on ports, which services and containers exist, where they connect, domains and certificates, firewall rules, detected findings.",
		"Answer in English. Do not retell the map — the reader already sees it. Talk about what is missing from it or contradicts itself in it.",
		"Rely only on the given data; if something is not visible, say so instead of assuming.",
		"Structure: sections starting with '## ':",
		"## Single points of failure — what one failure takes down: a single instance, a shared database, one disk.",
		"## Needless exposure — what is reachable from outside without need, where TLS is missing, where an admin panel is open to all.",
		"## Inconsistencies — an upstream pointing nowhere, a certificate for the wrong domain, a firewall rule contradicting a published port.",
		"## Where to start — 3-5 items in order of importance, each naming a concrete node from the map.",
		"If a section has nothing to report, write 'nothing to report' instead of inventing something.",
	}, "\n")
}

// SystemFor — стандартная инструкция по виду разбора.
func SystemFor(kind string, lang msgs.Lang) string {
	return systemPromptFor(PromptKindFor(kind), lang)
}

// Виды редактируемых инструкций: одна для разбора находок (finding, vuln,
// malware, event, job-error), другая для архитектурного разбора карты.
const (
	PromptFinding = "finding"
	PromptMap     = "map"
	PromptConfig  = "config"
	PromptIP      = "ip"
	// PromptMonitoring — планирование ресурсов по «Мониторингу» хаба.
	PromptMonitoring = "monitoring"
)

// PromptKinds — в порядке показа в настройках.
var PromptKinds = []string{PromptFinding, PromptMap, PromptConfig, PromptIP, PromptMonitoring}

// PromptKindFor — какая инструкция нужна виду разбора.
func PromptKindFor(kind string) string {
	switch kind {
	case KindMap, KindHubReview:
		return PromptMap
	case KindConfig:
		return PromptConfig
	case KindIP:
		return PromptIP
	case KindMonitoring:
		return PromptMonitoring
	default:
		return PromptFinding
	}
}

func systemPromptFor(promptKind string, lang msgs.Lang) string {
	switch promptKind {
	case PromptMap:
		return mapSystemPrompt(lang)
	case PromptConfig:
		return configSystemPrompt(lang)
	case PromptIP:
		return ipSystemPrompt(lang)
	case PromptMonitoring:
		return monitoringSystemPrompt(lang)
	default:
		return systemPrompt(lang)
	}
}

// configSystemPrompt — инструкция помощи по конфигурации: файл или
// программа целиком, а не одна находка.
func configSystemPrompt(lang msgs.Lang) string {
	if lang != msgs.EN {
		return strings.Join([]string{
			"Ты помогаешь системному администратору с конфигурацией сервиса на его Linux-сервере (nginx, haproxy, caddy, sshd, systemd, netplan, sysctl, cron, docker compose, libvirt и т. п.).",
			"Отвечай по-русски, коротко и по делу. Опирайся на приведённый текст конфигурации и контекст хоста; не придумывай директив, которых в этой программе нет.",
			"Пароли и ключи в тексте заменены на <secret> — не проси их и не подставляй.",
			"Структура ответа — ровно три раздела, каждый начинается со строки «## »:",
			"## Что настроено — по сути, 3–6 предложений: за что отвечает файл и что в нём главное.",
			"## Что стоит поправить — небезопасное, устаревшее, противоречивое, с номерами строк, если они есть; если замечаний нет — так и напиши.",
			"## Пример — готовый фрагмент конфигурации под вопрос оператора (если вопроса нет — типовой улучшенный вариант), в блоке ```, который можно вставить как есть.",
			"Не советуй действий, которые уронят доступ к серверу, без явного предупреждения.",
		}, "\n")
	}
	return strings.Join([]string{
		"You help a system administrator with a service configuration on their Linux server (nginx, haproxy, caddy, sshd, systemd, netplan, sysctl, cron, docker compose, libvirt and the like).",
		"Answer in English, briefly and to the point. Rely on the given configuration text and host context; do not invent directives this program does not have.",
		"Passwords and keys in the text are replaced with <secret> — do not ask for them and do not fill them in.",
		"Structure: exactly three sections, each starting with a '## ' line:",
		"## What is configured — the essence, 3-6 sentences: what the file is responsible for and what matters in it.",
		"## What to fix — insecure, deprecated, contradictory, with line numbers where available; if nothing, say so.",
		"## Example — a ready configuration fragment for the operator's question (a typical improved variant if there is no question), in a ``` block that can be pasted as is.",
		"Never suggest actions that would cut off access to the server without an explicit warning.",
	}, "\n")
}

// ipSystemPrompt — инструкция проверки внешнего адреса.
func ipSystemPrompt(lang msgs.Lang) string {
	if lang != msgs.EN {
		return strings.Join([]string{
			"Ты помогаешь системному администратору оценить внешний IP-адрес, который появился в оповещениях о его серверах (баны fail2ban, попытки входа).",
			"Отвечай по-русски, коротко и по делу. Опирайся на приведённые факты: где и за что адрес банили, строки журналов, обратный DNS, тип адреса.",
			"Ты не можешь сам проверить адрес во внешних базах — не выдумывай владельца, страну и репутацию, если их нет в фактах; можно назвать, где это посмотреть.",
			"Структура ответа — ровно три раздела, каждый начинается со строки «## »:",
			"## Кто это — что видно по фактам: сканер, перебор паролей, бот, похоже на легитимного клиента или на свой адрес.",
			"## Насколько опасен — насколько настойчив, на сколько хостов ходил, есть ли признаки успешного входа.",
			"## Что сделать — банить ли на всех хостах и на какой срок, что ещё проверить; команды — в блоке ```bash```.",
			"Если адрес похож на свой (адрес хаба, офиса, мониторинга) — прямо предупреди, что бан отрежет доступ.",
		}, "\n")
	}
	return strings.Join([]string{
		"You help a system administrator assess an external IP address that showed up in alerts about their servers (fail2ban bans, login attempts).",
		"Answer in English, briefly and to the point. Rely on the given facts: where and why the address was banned, log lines, reverse DNS, the kind of address.",
		"You cannot look the address up in external databases yourself — do not invent the owner, country or reputation if the facts do not have them; you may name where to look.",
		"Structure: exactly three sections, each starting with a '## ' line:",
		"## Who this is — what the facts show: a scanner, password guessing, a bot, something that looks like a legitimate client or one's own address.",
		"## How dangerous — how persistent, how many hosts it went after, any signs of a successful login.",
		"## What to do — whether to ban it on all hosts and for how long, what else to check; commands in a ```bash``` block.",
		"If the address looks like one's own (the hub, an office, monitoring), warn explicitly that a ban would cut off access.",
	}, "\n")
}

// MaxConfigContent — сколько текста конфигурации уходит модели; дальше
// обрезка с пометкой: контекст не резиновый, а суть файла обычно в начале.
const MaxConfigContent = 32 << 10

// SystemWith — инструкция с учётом правок оператора: overrides хранит
// текст по ключу PromptKey; пусто — стандартная.
func SystemWith(overrides map[string]string, kind string, lang msgs.Lang) string {
	pk := PromptKindFor(kind)
	if text := strings.TrimSpace(overrides[PromptKey(pk, lang)]); text != "" {
		return text
	}
	return systemPromptFor(pk, lang)
}

// PromptKey — ключ правленой инструкции: «finding/ru», «map/en».
func PromptKey(promptKind string, lang msgs.Lang) string {
	l := "ru"
	if lang == msgs.EN {
		l = "en"
	}
	return promptKind + "/" + l
}

// FindingKey — устойчивый ключ находки без привязки к хосту: по нему
// ответ сохраняется у строки и та же находка на другом хосте узнаётся
// как «уже разбиралась». Подробности и подсказка в ключ не входят:
// они меняются от сканирования к сканированию (номера PID, даты), а
// проблема остаётся той же.
func FindingKey(kind, title, object, file string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(kind), strings.TrimSpace(title), strings.TrimSpace(object), strings.TrimSpace(file),
	}, "\x00")))
	return hex.EncodeToString(h[:16])
}

// FindingContext — контекст одной находки для промпта. Поля
// необязательные: чем больше известно, тем конкретнее ответ, но и
// одного заголовка достаточно.
type FindingContext struct {
	Kind       string
	Title      string
	Detail     string
	Suggestion string
	Severity   string
	Service    string
	Object     string
	File       string
	Line       int
	// Host — краткое описание хоста: ОС, ядро, роль.
	Host string
	// Around — что рядом: слушатели, контейнеры, правила firewall —
	// ровно то, из чего модель может сделать вывод «грозит здесь».
	Around []string
	// Diff — для ошибки правки конфигурации: что именно меняли (unified
	// diff «на диске → черновик»), а не весь файл.
	Diff string
	// Output — вывод проверки (валидатора, apply) — сама ошибка.
	Output string
	// Content — текст конфигурации для KindConfig (секреты вырезаются в
	// Mapper.Hide, обрезка — в UserPrompt).
	Content string
	// Question — что нужно оператору от примера (KindConfig); пусто —
	// обзор и типовой пример.
	Question string
}

// UserPrompt собирает текст запроса. Пустые поля пропускаются: строка
// «Файл: (пусто)» только сбивает модель.
func UserPrompt(c FindingContext, lang msgs.Lang) string {
	ru := lang != msgs.EN
	label := func(ruText, enText string) string {
		if ru {
			return ruText
		}
		return enText
	}
	var b strings.Builder
	add := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		fmt.Fprintf(&b, "%s: %s\n", name, strings.TrimSpace(value))
	}
	if c.Kind == KindIP {
		add(label("Адрес", "Address"), c.Object)
	} else {
		add(label("Находка", "Finding"), c.Title)
	}
	add(label("Подробности", "Details"), c.Detail)
	add(label("Серьёзность", "Severity"), c.Severity)
	add(label("Сервис", "Service"), c.Service)
	if c.Kind != KindIP {
		add(label("Объект", "Object"), c.Object)
	}
	if c.File != "" {
		if c.Line > 0 {
			add(label("Файл", "File"), fmt.Sprintf("%s:%d", c.File, c.Line))
		} else {
			add(label("Файл", "File"), c.File)
		}
	}
	add(label("Подсказка nkt", "nkt suggestion"), c.Suggestion)
	add(label("Хост", "Host"), c.Host)
	if strings.TrimSpace(c.Output) != "" {
		fmt.Fprintf(&b, "%s:\n```\n%s\n```\n", label("Вывод проверки", "Check output"), strings.TrimSpace(c.Output))
	}
	if strings.TrimSpace(c.Diff) != "" {
		fmt.Fprintf(&b, "%s:\n```diff\n%s\n```\n", label("Дифф правки", "Diff of the edit"), strings.TrimSpace(c.Diff))
	}
	if len(c.Around) > 0 {
		around := label("Рядом на хосте", "Nearby on the host")
		if c.Kind == KindIP {
			around = label("Что известно об адресе", "What is known about the address")
		}
		fmt.Fprintf(&b, "%s:\n", around)
		for _, line := range c.Around {
			if strings.TrimSpace(line) == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(line))
		}
	}
	if c.Kind == KindConfig {
		content := c.Content
		truncated := false
		if len(content) > MaxConfigContent {
			content = content[:MaxConfigContent]
			truncated = true
		}
		if strings.TrimSpace(content) != "" {
			fmt.Fprintf(&b, "%s:\n```\n%s\n```\n", label("Конфигурация", "Configuration"), strings.TrimSpace(content))
			if truncated {
				b.WriteString(label("(файл обрезан: показано начало)\n", "(file truncated: the beginning is shown)\n"))
			}
		}
		if q := strings.TrimSpace(c.Question); q != "" {
			fmt.Fprintf(&b, "%s: %s\n", label("Вопрос оператора", "Operator's question"), q)
		} else {
			b.WriteString(label("Вопроса нет: дай обзор и типовой улучшенный пример.\n", "No question: give an overview and a typical improved example.\n"))
		}
	}
	if c.Kind == KindIP {
		b.WriteString(label("Задача: оцени этот адрес и скажи, банить ли его на всех хостах.\n",
			"Task: assess this address and say whether to ban it on all hosts.\n"))
	}
	if c.Kind == KindConfigError {
		// Своя постановка задачи: это не находка сканера, а отказ при
		// записи — нужен не риск, а исправленный фрагмент.
		b.WriteString(label(
			"Задача: правка файла не прошла проверку и откатилась. Объясни, что именно не так (с номером строки, если он есть в выводе), и покажи исправленный фрагмент.\n",
			"Task: the edit failed validation and was rolled back. Explain exactly what is wrong (with the line number if the output has one) and show the corrected fragment.\n"))
	}
	return strings.TrimSpace(b.String())
}

// MapPrompt собирает текст запроса для архитектурного разбора.
func MapPrompt(lines []string, lang msgs.Lang) string {
	head := "Карта ресурсов:"
	if lang == msgs.EN {
		head = "Resource map:"
	}
	var b strings.Builder
	b.WriteString(head + "\n")
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		fmt.Fprintf(&b, "%s\n", strings.TrimSpace(l))
	}
	return strings.TrimSpace(b.String())
}

// monitoringSystemPrompt — инструкция разбора «Мониторинга» хаба:
// прогнозы и сводки по хостам → что делать и в каком порядке.
func monitoringSystemPrompt(lang msgs.Lang) string {
	if lang != msgs.EN {
		return strings.Join([]string{
			"Ты помогаешь системному администратору планировать ресурсы и доступность его серверов. На вход — прогнозы и подсказки по трендам (заполнение дисков, память, CPU, похожие на утечку рост памяти контейнеров, падение доступности целей) и сводка по хостам за период.",
			"Отвечай по-русски, коротко и по делу. Опирайся только на приведённые цифры; прогнозы — оценки по тренду, а не факт: учитывай это и не преувеличивай.",
			"Структура ответа — ровно три раздела, каждый начинается со строки «## »:",
			"## Главное — что из этого действительно требует внимания и насколько срочно, 2–5 пунктов по убыванию срочности.",
			"## Что сделать — конкретные шаги по каждому пункту: что проверить, что почистить или перенести, когда (с учётом тихого окна, если оно есть); команды — в блоке ```bash```.",
			"## Что наблюдать дальше — какие показатели и пороги держать в поле зрения, чтобы решение не опоздало.",
			"Не советуй действий, которые уронят доступ к серверу или данные, без явного предупреждения.",
		}, "\n")
	}
	return strings.Join([]string{
		"You help a system administrator plan capacity and availability of their servers. The input is trend-based forecasts and hints (disks filling up, memory, CPU, container memory growth that looks like a leak, target availability drops) and a per-host summary for the period.",
		"Answer in English, briefly and to the point. Rely only on the given numbers; forecasts are trend estimates, not facts: keep that in mind and do not exaggerate.",
		"Structure: exactly three sections, each starting with a '## ' line:",
		"## What matters — what actually needs attention and how urgently, 2-5 items, most urgent first.",
		"## What to do — concrete steps for each item: what to check, what to clean up or move, when (using the quiet window if there is one); commands in a ```bash``` block.",
		"## What to watch next — which numbers and thresholds to keep an eye on so the decision is not late.",
		"Never suggest actions that would cut off access to the server or lose data without an explicit warning.",
	}, "\n")
}
