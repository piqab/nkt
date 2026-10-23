package hub

import (
	"strings"
	"unicode"
)

// translitRU — кириллица латиницей для имён файлов при скачивании.
var translitRU = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh", 'з': "z", 'и': "i",
	'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t",
	'у': "u", 'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch", 'ъ': "", 'ы': "y", 'ь': "",
	'э': "e", 'ю': "yu", 'я': "ya",
}

// asciiFileName — имя файла только из латиницы, цифр, «.», «_» и «-»:
// кириллица — транслитом, остальное — «-»; пустое — fallback.
func asciiFileName(name, fallback string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(name) {
		lower := unicode.ToLower(r)
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-'):
			b.WriteRune(r)
			dash = false
		case translitRU[lower] != "" || lower == 'ъ' || lower == 'ь':
			t := translitRU[lower]
			if unicode.IsUpper(r) && t != "" {
				t = strings.ToUpper(t[:1]) + t[1:]
			}
			b.WriteString(t)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	if len(out) > 100 {
		out = out[:100]
	}
	if out == "" {
		return fallback
	}
	return out
}
