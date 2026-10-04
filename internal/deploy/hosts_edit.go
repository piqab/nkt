package deploy

import (
	"regexp"
	"strings"
)

// Правка списка хостов compose в тексте описания: «убрать стек с хоста»
// оставляет конвейер, но этот хост из hosts: уходит. Текст правится
// построчно — комментарии и порядок полей сохраняются, как при ручной
// правке в редакторе.

var (
	hostsFlowRe  = regexp.MustCompile(`^([ \t]+)hosts:[ \t]*\[([^\]]*)\]([ \t]*#.*)?$`)
	hostsBlockRe = regexp.MustCompile(`^([ \t]+)hosts:[ \t]*(#.*)?$`)
	listItemRe   = regexp.MustCompile(`^([ \t]*)-[ \t]*([^#]*?)[ \t]*(#.*)?$`)
)

// WithoutHosts — описание без хостов drop в compose.hosts (имена — без
// учёта регистра). ok=false — список хостов не найден (стек выбирается
// группой) или в нём не осталось бы ни одного хоста.
func WithoutHosts(content string, drop []string) (string, bool) {
	gone := func(n string) bool {
		n = strings.Trim(strings.TrimSpace(n), `"'`)
		for _, d := range drop {
			if strings.EqualFold(n, strings.TrimSpace(d)) {
				return true
			}
		}
		return false
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if m := hostsFlowRe.FindStringSubmatch(l); m != nil {
			var keep []string
			for _, n := range strings.Split(m[2], ",") {
				if n = strings.TrimSpace(n); n != "" && !gone(n) {
					keep = append(keep, n)
				}
			}
			if len(keep) == 0 {
				return content, false
			}
			lines[i] = m[1] + "hosts: [" + strings.Join(keep, ", ") + "]" + m[3]
			return strings.Join(lines, "\n"), true
		}
		if m := hostsBlockRe.FindStringSubmatch(l); m != nil {
			indent := len(m[1])
			var out []string
			left := 0
			j := i + 1
			for ; j < len(lines); j++ {
				it := listItemRe.FindStringSubmatch(lines[j])
				if it == nil || len(it[1]) < indent {
					break
				}
				if gone(it[2]) {
					continue
				}
				out = append(out, lines[j])
				left++
			}
			if left == 0 {
				return content, false
			}
			res := append(append(append([]string{}, lines[:i+1]...), out...), lines[j:]...)
			return strings.Join(res, "\n"), true
		}
	}
	return content, false
}
