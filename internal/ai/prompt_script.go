package ai

import (
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// Инструкции для ИИ в редакторе сценариев хаба. Справка языка и имена
// объектов хаба собирает вызывающий (hub): здесь — только правила.

// ScriptSystemPrompt — инструкция модели: mode — explain | comment | task.
func ScriptSystemPrompt(mode string, lang msgs.Lang, reference, names string) string {
	var b strings.Builder
	if lang == msgs.EN {
		b.WriteString(`You help write nkt hub scripts: a line-based language of the commands below, one line — one action, a block goes up to "end". The hub runs the script top to bottom with the same calls as the UI buttons and stops at the first error.

Rules:
- Use only the commands and syntax from the reference. Do not invent commands, options or shell commands.
- Refer to hosts, groups, profiles, pipelines, clusters and gateways by the names listed below; a new one only if the script creates it itself (host, group, k8s create, pipeline).
- Values the user did not give (addresses, domains, passwords) — "param NAME \"prompt\"" at the top, then ${NAME}; secrets — "set NAME ask". Never put real secrets in the text.
- Prefer safe steps: "wait" after installs and deploys, "migrate" with switch-fly/switch-pipeline when the stack is behind Traffic Fly.
- A stopped container or stack is brought back with start, not up: a stack deployed by a pipeline — "deploy PIPELINE start" (the same version; the pipeline's registry key for missing images), any other — "on HOST docker stack PATH start" (no registry). "docker stack … up" pulls images and fails on a private registry without a key. To stop — "deploy PIPELINE stop" or "docker stack PATH stop". A pipeline marked "stopped" was stopped on purpose — do not offer to start it unless asked.
`)
	} else {
		b.WriteString(`Ты помогаешь писать сценарии хаба nkt: построчный язык из команд ниже, одна строка — одно действие, блок — до «end». Хаб выполняет сценарий по порядку теми же вызовами, что кнопки интерфейса, и останавливается на первой ошибке.

Правила:
- Только команды и синтаксис из справки. Не выдумывай команды, параметры и команды оболочки.
- Хосты, группы, профили, конвейеры, кластеры и шлюзы — по именам из списка ниже; новые — только если сценарий сам их заводит (host, group, k8s create, pipeline).
- Чего пользователь не сказал (адреса, домены, пароли) — «param ИМЯ "подсказка"» в начале и ${ИМЯ}; секреты — «set ИМЯ ask». Настоящих секретов в тексте не пиши.
- Безопасные шаги: «wait» после установки и выкладки, «migrate» с switch-fly/switch-pipeline, если стек за Traffic Fly.
- Остановленный контейнер или стек поднимают через start, а не up: стек, выложенный конвейером, — «deploy КОНВЕЙЕР start» (та же версия; недостающие образы — с ключом registry конвейера), прочий — «on ХОСТ docker stack ПУТЬ start» (без registry). «docker stack … up» скачивает образы и падает на закрытом registry без ключа. Остановить — «deploy КОНВЕЙЕР stop» или «docker stack ПУТЬ stop». Конвейер с пометкой «остановлена» остановили намеренно — не предлагай запускать, если не просят.
`)
	}
	switch mode {
	case "explain":
		if lang == msgs.EN {
			b.WriteString("\nTask: explain the given lines plainly — what each step does on the hosts, in what order, what changes and what is risky (stops a service, deletes data, opens a port). Short paragraphs, no rewrite of the script unless there is a mistake; then show the fixed lines in a ``` block.\n")
		} else {
			b.WriteString("\nЗадача: объясни данные строки простыми словами — что делает каждый шаг на хостах, в каком порядке, что изменится и чем рискованно (остановит службу, удалит данные, откроет порт). Короткие абзацы; сценарий не переписывай, разве что есть ошибка — тогда исправленные строки в блоке ```.\n")
		}
	case "comment":
		if lang == msgs.EN {
			b.WriteString("\nTask: the script has lines \"# AI: …\" (or \"# ИИ: …\") — tasks. Under each such line write the commands that do it; keep the comment line and everything else as is. Answer with the whole script in one ``` block and nothing else.\n")
		} else {
			b.WriteString("\nЗадача: в сценарии есть строки «# ИИ: …» (или «# AI: …») — задания. Под каждой такой строкой напиши команды, которые это делают; строку комментария и всё остальное оставь как есть. Ответ — весь сценарий одним блоком ``` и больше ничего.\n")
		}
	default:
		if lang == msgs.EN {
			b.WriteString("\nTask: write a whole script for the user's task. A comment line at the top with what it does, comments before the parts. Answer with the script in one ``` block and nothing else.\n")
		} else {
			b.WriteString("\nЗадача: напиши сценарий целиком под задачу пользователя. В начале строка-комментарий, что он делает, перед частями — комментарии. Ответ — сценарий одним блоком ``` и больше ничего.\n")
		}
	}
	fmt.Fprintf(&b, "\n# Objects on the hub\n%s\n# Language reference\n%s", names, reference)
	return b.String()
}

// ScriptUserPrompt — запрос пользователя.
func ScriptUserPrompt(mode string, lang msgs.Lang, content, selection, task string) string {
	en := lang == msgs.EN
	switch mode {
	case "explain":
		part := selection
		if strings.TrimSpace(part) == "" {
			part = content
		}
		if en {
			return "Explain:\n```\n" + part + "\n```\n\nThe whole script for context:\n```\n" + content + "\n```"
		}
		return "Объясни:\n```\n" + part + "\n```\n\nВесь сценарий — для контекста:\n```\n" + content + "\n```"
	case "comment":
		if en {
			return "The script:\n```\n" + content + "\n```"
		}
		return "Сценарий:\n```\n" + content + "\n```"
	}
	if en {
		out := "Task: " + task
		if strings.TrimSpace(content) != "" {
			out += "\n\nThe current editor text (keep what still fits):\n```\n" + content + "\n```"
		}
		return out
	}
	out := "Задача: " + task
	if strings.TrimSpace(content) != "" {
		out += "\n\nТекущий текст в редакторе (оставь, что подходит):\n```\n" + content + "\n```"
	}
	return out
}

// ScriptFixPrompt — повторная попытка: текст и ошибки «Проверить».
func ScriptFixPrompt(lang msgs.Lang, text, issues string) string {
	if lang == msgs.EN {
		return "The hub's check found errors in your script:\n" + issues + "\nFix them. The script:\n```\n" + text + "```\nAnswer with the whole fixed script in one ``` block and nothing else."
	}
	return "Проверка хаба нашла ошибки в твоём сценарии:\n" + issues + "\nИсправь их. Сценарий:\n```\n" + text + "```\nОтвет — весь исправленный сценарий одним блоком ``` и больше ничего."
}

// FixScriptHint — к разбору находки на хосте: исправление ещё и сценарием
// nkt в отдельном блоке ```nkt (только командами из списка).
func FixScriptHint(lang msgs.Lang, host, syntax string) string {
	if lang == msgs.EN {
		return "\n\nIf the fix can be done with nkt script commands (below), add at the very end a block ```nkt with such a script for host " + host +
			" — only these commands, one line per action, a comment line on top. No shell commands inside the block. If it cannot be done with them, do not add the block.\n" + syntax
	}
	return "\n\nЕсли исправление делается командами сценария nkt (ниже), добавь в самом конце блок ```nkt с таким сценарием для хоста " + host +
		" — только этими командами, по строке на действие, сверху строка-комментарий. Команд оболочки в блоке быть не должно. Если ими не сделать — блок не добавляй.\n" + syntax
}
