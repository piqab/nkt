package ai

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Псевдонимизация: что именно уходит к модели.
//
// Модели нужна структура проблемы — «сервис слушает наружу», «апстрим
// указывает в никуда», — а не ваши адреса и домены. Перед отправкой они
// заменяются на устойчивые псевдонимы (host-1, 10.0.0.1 → ip-1,
// example.com → domain-1), а в ответе подставляются обратно: оператор
// читает про свой хост, провайдер видел обезличенный текст.
//
// Замена односторонняя и живёт в памяти одного запроса: словарь никуда
// не сохраняется. Совпадения ищутся по длине от большего к меньшему,
// иначе «app.example.com» превратился бы в «app.domain-1».

// Mapper — словарь замен одного запроса.
type Mapper struct {
	enabled bool
	to      map[string]string // настоящее → псевдоним
	back    map[string]string // псевдоним → настоящее
	counts  map[string]int
}

// NewMapper — словарь; enabled=false делает все методы прозрачными.
func NewMapper(enabled bool) *Mapper {
	return &Mapper{enabled: enabled, to: map[string]string{}, back: map[string]string{}, counts: map[string]int{}}
}

var (
	ipv4Re   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	domainRe = regexp.MustCompile(`\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}\b`)
	emailRe  = regexp.MustCompile(`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`)
)

// Learn добавляет в словарь то, что известно заранее: имена хостов и
// машин из списка хаба. Они не похожи ни на адрес, ни на домен —
// регулярным выражением их не найти.
func (m *Mapper) Learn(kind string, names []string) {
	if !m.enabled {
		return
	}
	sorted := append([]string(nil), names...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, name := range sorted {
		if len(strings.TrimSpace(name)) < 3 {
			continue
		}
		m.alias(kind, name)
	}
}

func (m *Mapper) alias(kind, real string) string {
	if a, ok := m.to[real]; ok {
		return a
	}
	m.counts[kind]++
	a := fmt.Sprintf("%s-%d", kind, m.counts[kind])
	m.to[real] = a
	m.back[a] = real
	return a
}

// Hide заменяет в тексте всё известное и всё, что похоже на адрес,
// домен или почту.
func (m *Mapper) Hide(text string) string {
	if !m.enabled || text == "" {
		return text
	}
	// Сначала выученные имена — они длиннее и конкретнее.
	keys := make([]string, 0, len(m.to))
	for k := range m.to {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		text = strings.ReplaceAll(text, k, m.to[k])
	}
	text = emailRe.ReplaceAllStringFunc(text, func(s string) string { return m.alias("email", s) })
	text = ipv4Re.ReplaceAllStringFunc(text, func(s string) string {
		// Адреса документации и loopback оставляем как есть: они ничего
		// не выдают, а в тексте помогают понять, о чём речь.
		if strings.HasPrefix(s, "127.") || s == "0.0.0.0" {
			return s
		}
		return m.alias("ip", s)
	})
	text = domainRe.ReplaceAllStringFunc(text, func(s string) string {
		if strings.HasSuffix(s, ".service") || strings.HasSuffix(s, ".conf") || strings.HasSuffix(s, ".yml") ||
			strings.HasSuffix(s, ".yaml") || strings.HasSuffix(s, ".json") || strings.HasSuffix(s, ".sock") ||
			strings.HasSuffix(s, ".pem") || strings.HasSuffix(s, ".key") || strings.HasSuffix(s, ".log") {
			// Это имена файлов и юнитов, а не домены: они и есть суть
			// находки, прятать их — оставить модель без предмета разговора.
			return s
		}
		return m.alias("domain", s)
	})
	return text
}

// Reveal возвращает настоящие значения в ответе модели.
func (m *Mapper) Reveal(text string) string {
	if !m.enabled || text == "" {
		return text
	}
	keys := make([]string, 0, len(m.back))
	for k := range m.back {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, k := range keys {
		text = strings.ReplaceAll(text, k, m.back[k])
	}
	return text
}

// Size — сколько значений спрятано (для «показать запрос» в интерфейсе).
func (m *Mapper) Size() int { return len(m.to) }
