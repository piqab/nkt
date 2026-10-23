package profile

import (
	"fmt"
	"strings"

	"github.com/piqab/nkt/internal/msgs"
)

// SectionDoc — раздел профиля в справке: тексты — ключи каталога msgs
// (profile.docs.section.<Key>.title|body), пример — YAML без комментариев:
// он один на оба языка.
type SectionDoc struct {
	Key     string `json:"key"`
	Example string `json:"example"`
}

// Sections — разделы формата в порядке справки. Пример каждого раздела
// разбирается Parse (TestDocExamplesParse): справка не покажет то, что
// не сохранится.
var Sections = []SectionDoc{
	{Key: "head", Example: "version: 1\nname: web-server"},
	{Key: "packages", Example: "packages:\n  - nginx\n  - btop"},
	{Key: "services", Example: "services:\n  nginx:\n    enabled: true\n    active: true\n  apache2:\n    active: false"},
	{Key: "files", Example: "files:\n  - path: /etc/motd\n    mode: \"0644\"\n    content: |\n      Managed by nkt"},
	{Key: "firewall", Example: "firewall:\n  allow:\n    - port: 443\n    - port: 5432\n      proto: tcp\n      from: 10.0.0.0/24"},
	{Key: "users", Example: "users:\n  - name: deploy\n    sudo: true\n    keys:\n      - \"ssh-ed25519 AAAA... deploy@laptop\""},
	{Key: "system", Example: "system:\n  hostname: web-1\n  timezone: Europe/Moscow"},
	{Key: "compose", Example: "compose:\n  - name: shop\n    up: true\n    content: |\n      services:\n        web:\n          image: nginx:1.27\n          ports: [\"8080:80\"]"},
}

// ExampleDoc — готовый профиль целиком («Готовые профили ▾» в окне
// правки и страница сайта).
type ExampleDoc struct {
	Key     string `json:"key"`
	Content string `json:"content"`
}

// Examples — готовые профили; каждый проходит Parse.
var Examples = []ExampleDoc{
	{Key: "base", Content: `version: 1
name: base
packages:
  - htop
  - curl
  - ncdu
system:
  timezone: Europe/Moscow
users:
  - name: deploy
    keys:
      - "ssh-ed25519 AAAA... deploy@laptop"
firewall:
  allow:
    - port: 22
`},
	{Key: "web", Content: `version: 1
name: web-server
packages:
  - nginx
services:
  nginx:
    enabled: true
    active: true
firewall:
  allow:
    - port: 80
    - port: 443
files:
  - path: /etc/motd
    mode: "0644"
    content: |
      web server, managed by nkt
`},
	{Key: "docker", Content: `version: 1
name: docker-host
compose:
  - name: web
    up: true
    content: |
      services:
        web:
          image: nginx:1.27
          ports: ["8080:80"]
          restart: unless-stopped
`},
}

var limitKeys = []string{"packages", "files", "order", "volumes"}

// Help — справка формата для встроенного окна («Из nkt»): те же тексты и
// примеры, что на странице сайта.
type Help struct {
	Intro    []string         `json:"intro"`
	Sections []HelpSection    `json:"sections"`
	Limits   HelpLimits       `json:"limits"`
	Examples []HelpExampleDoc `json:"examples"`
}

type HelpSection struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Example string `json:"example"`
}

type HelpLimits struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
}

type HelpExampleDoc struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Content string `json:"content"`
}

// HelpFor собирает справку на языке lang.
func HelpFor(lang msgs.Lang) Help {
	tr := func(key string) string { return msgs.T(lang, key) }
	h := Help{Intro: []string{tr("profile.docs.intro"), tr("profile.docs.additive"), tr("profile.docs.flow")}}
	for _, s := range Sections {
		h.Sections = append(h.Sections, HelpSection{Key: s.Key, Title: tr("profile.docs.section." + s.Key + ".title"),
			Body: tr("profile.docs.section." + s.Key + ".body"), Example: s.Example})
	}
	h.Limits.Title = tr("profile.docs.limits.title")
	for _, k := range limitKeys {
		h.Limits.Items = append(h.Limits.Items, tr("profile.docs.limits."+k))
	}
	for _, e := range Examples {
		h.Examples = append(h.Examples, HelpExampleDoc{Key: e.Key, Title: tr("profile.docs.ex." + e.Key + ".title"),
			Summary: tr("profile.docs.ex." + e.Key + ".summary"), Content: e.Content})
	}
	return h
}

// DocsMarkdown — страница сайта «Формат профиля» (site/guide/
// profile-format.md и en/): из тех же Sections и Examples, что встроенная
// справка и «Готовые профили ▾»; сверяет TestProfileFormatPage.
func DocsMarkdown(lang msgs.Lang) string {
	tr := func(key string) string { return msgs.T(lang, key) }
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ") }
	first := func(s string) string {
		if i := strings.Index(s, ". "); i > 0 {
			return s[:i+1]
		}
		return s
	}
	// Проза идёт в markdown сайта: «<имя>» vitepress принял бы за тег.
	prose := strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace
	h := HelpFor(lang)
	for i := range h.Intro {
		h.Intro[i] = prose(h.Intro[i])
	}
	for i := range h.Sections {
		h.Sections[i].Body = prose(h.Sections[i].Body)
	}
	for i := range h.Limits.Items {
		h.Limits.Items[i] = prose(h.Limits.Items[i])
	}
	for i := range h.Examples {
		h.Examples[i].Summary = prose(h.Examples[i].Summary)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %s\n---\n\n", tr("profile.docs.title"))
	b.WriteString("<!-- " + tr("profile.docs.generated") + " -->\n\n")
	fmt.Fprintf(&b, "# %s\n\n", tr("profile.docs.title"))
	for _, p := range h.Intro {
		b.WriteString(p + "\n\n")
	}
	b.WriteString(tr("profile.docs.seeAlso") + "\n\n")
	fmt.Fprintf(&b, "## %s\n\n| %s | %s |\n|---|---|\n", tr("profile.docs.overview"), tr("profile.docs.colSection"), tr("profile.docs.colWhat"))
	for _, s := range h.Sections {
		fmt.Fprintf(&b, "| [%s](#sec-%s) | %s |\n", cell(s.Title), s.Key, cell(first(s.Body)))
	}
	fmt.Fprintf(&b, "\n## %s\n\n", tr("profile.docs.sections"))
	for _, s := range h.Sections {
		fmt.Fprintf(&b, "### %s {#sec-%s}\n\n%s\n\n%s:\n\n```yaml\n%s\n```\n\n", s.Title, s.Key, s.Body, tr("profile.docs.example"), s.Example)
	}
	fmt.Fprintf(&b, "## %s\n\n", h.Limits.Title)
	for _, it := range h.Limits.Items {
		b.WriteString("- " + it + "\n")
	}
	fmt.Fprintf(&b, "\n## %s\n\n", tr("profile.docs.examples"))
	for _, e := range h.Examples {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n```yaml\n%s```\n\n", e.Title, e.Summary, e.Content)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
