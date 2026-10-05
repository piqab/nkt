package main

import (
	"os"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// cliLang — язык командной строки по локали терминала: LC_ALL, затем
// LC_MESSAGES, затем LANG (первая заданная). Русский — для ru*, иначе
// английский: у службы под systemd локаль обычно не задана, и её
// сообщения об ошибках запуска уходят в журнал по-английски, как и он сам.
func cliLang() msgs.Lang {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(name); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "ru") {
				return msgs.RU
			}
			return msgs.EN
		}
	}
	return msgs.EN
}

// cli — текст каталога (ключи cli.*) на языке командной строки.
func cli(key string, args ...any) string {
	return msgs.T(cliLang(), key, args...)
}
