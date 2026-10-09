package deploy

import (
	"regexp"
	"sort"
	"strings"
)

// Имена образов стека: подстановка переменных .env так, как её делает
// docker compose, и проверка, что получившееся имя движок примет.

// ExpandVars подставляет переменные в строку compose-файла: $VAR, ${VAR},
// ${VAR:-по умолчанию}, ${VAR-…}, ${VAR:?…}, ${VAR?…}, ${VAR:+…}, ${VAR+…};
// $$ — знак доллара. Значение по умолчанию само может содержать
// переменные. Переменной нет в vars — пустая строка, как у compose.
func ExpandVars(s string, vars map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		switch next := s[i+1]; {
		case next == '$':
			b.WriteByte('$')
			i++
		case next == '{':
			end := closingBrace(s, i+2)
			if end < 0 {
				b.WriteString(s[i:])
				return b.String()
			}
			b.WriteString(expandBraced(s[i+2:end], vars))
			i = end
		case isVarStart(next):
			j := i + 1
			for j < len(s) && isVarChar(s[j]) {
				j++
			}
			b.WriteString(vars[s[i+1:j]])
			i = j - 1
		default:
			b.WriteByte('$')
		}
	}
	return b.String()
}

// closingBrace — индекс «}», закрывающей ${…} с учётом вложенных ${…}.
func closingBrace(s string, from int) int {
	depth := 1
	for j := from; j < len(s); j++ {
		switch {
		case s[j] == '$' && j+1 < len(s) && s[j+1] == '{':
			depth++
			j++
		case s[j] == '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// expandBraced — содержимое ${…}: имя и, может быть, модификатор.
func expandBraced(body string, vars map[string]string) string {
	n := 0
	for n < len(body) && isVarChar(body[n]) {
		n++
	}
	name, rest := body[:n], body[n:]
	val, set := vars[name]
	colon := strings.HasPrefix(rest, ":")
	op := strings.TrimPrefix(rest, ":")
	if op == "" {
		return val
	}
	arg := ExpandVars(op[1:], vars)
	// С двоеточием пустое значение — то же, что не задано.
	present := set && (!colon || val != "")
	switch op[0] {
	case '-':
		if !present {
			return arg
		}
	case '+':
		if present {
			return arg
		}
		return ""
	}
	// «?» — compose остановится с сообщением; имя образа тогда пустое.
	return val
}

func isVarStart(c byte) bool { return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') }

func isVarChar(c byte) bool { return isVarStart(c) || ('0' <= c && c <= '9') }

// ComposeImagesEnv — образы стека (как ComposeImages) с переменными .env:
// по ним хаб подбирает ключи registry.
func ComposeImagesEnv(text string, vars map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, img := range ComposeImages(text) {
		if img = strings.TrimSpace(ExpandVars(img, vars)); img != "" && !seen[img] {
			seen[img] = true
			out = append(out, img)
		}
	}
	sort.Strings(out)
	return out
}

// imageRefRe — имя образа, которое примет docker: [адрес[:порт]/]путь
// строчными, [:тег], [@sha256:…].
var imageRefRe = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?(?::[0-9]+)?/)?` +
	`[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*` +
	`(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(?:@sha256:[0-9a-f]{64})?$`)

// Что не так с именем образа (ImageRefProblem).
const (
	ImageRefScheme = "scheme" // http:// или https:// в начале
	ImageRefNoHost = "nohost" // начинается с «/» — пустая переменная адреса
	ImageRefBad    = "bad"    // прочее, что движок не примет
)

// ImageRefProblem — "" для допустимого имени образа, иначе ImageRef*.
func ImageRefProblem(img string) string {
	low := strings.ToLower(img)
	switch {
	case strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://"):
		return ImageRefScheme
	case strings.HasPrefix(img, "/"):
		return ImageRefNoHost
	case len(img) > 512 || !imageRefRe.MatchString(img):
		return ImageRefBad
	}
	return ""
}
