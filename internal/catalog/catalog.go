// Package catalog — каталог плагинов и примеров конвейеров (1.16.0).
//
// Плагины и примеры больше не встроены в бинарник nkt: CI собирает их из
// examples/ репозитория в каталог (index.json + файлы), подписывает его
// ключом ed25519 и выкладывает рядом с сайтом. Хаб скачивает каталог,
// проверяет подпись и SHA-256 каждого файла, хранит проверенную копию у
// себя и работает из неё — в том числе без интернета.
//
// Подменённый каталог — это чужой код на всех хостах, где стоят плагины,
// поэтому каталог без верной подписи доверенным ключом не принимается ни
// при скачивании, ни при загрузке архива, ни при чтении копии с диска.
package catalog

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// Format — версия формата index.json, которую понимает этот nkt.
const Format = 1

// Index — index.json каталога.
type Index struct {
	Format    int       `json:"format"`
	Commit    string    `json:"commit,omitempty"`
	Generated string    `json:"generated"`
	Plugins   []Plugin  `json:"plugins"`
	Examples  []Example `json:"examples"`
}

// FileRef — файл каталога: отпечаток и размер.
type FileRef struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Plugin — плагин, который хаб ставит на хост службой без Docker
// (examples/<key>/deploy/nkt-plugin.json).
type Plugin struct {
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	MinNKT      string            `json:"min_nkt,omitempty"`
	Protocol    int               `json:"protocol"`
	Title       map[string]string `json:"title"`
	Description map[string]string `json:"description"`
	Run         string            `json:"run"`
	File        string            `json:"file"`
	Port        int               `json:"port"`
	// Docker — плагину нужен доступ к Docker на хосте (служба в группе
	// docker — это права root): хаб показывает это до установки.
	Docker   bool      `json:"docker,omitempty"`
	Settings []Setting `json:"settings"`
	// Files — файлы плагина относительно examples/<key>/.
	Files map[string]FileRef `json:"files"`
	// Readme и Screens — справка и скриншоты по языкам (пути из Files).
	Readme  map[string]string   `json:"readme,omitempty"`
	Screens map[string][]string `json:"screens,omitempty"`
	// Tests — «passed»: тест на подделках прошёл в CI при сборке каталога.
	Tests string `json:"tests,omitempty"`
}

// Setting — поле формы установки: переменная окружения плагина.
type Setting struct {
	Env      string            `json:"env"`
	Title    map[string]string `json:"title"`
	Hint     map[string]string `json:"hint,omitempty"`
	Default  string            `json:"default,omitempty"`
	Secret   bool              `json:"secret,omitempty"`
	Required bool              `json:"required,omitempty"`
}

// Example — пример конвейера (examples/<key>/pipeline*.yaml) и описание
// для окна нового конвейера (nkt-example.json).
type Example struct {
	Key       string             `json:"key"`
	Title     map[string]string  `json:"title"`
	Files     map[string]FileRef `json:"files"`
	Readme    map[string]string  `json:"readme,omitempty"`
	Pipelines []string           `json:"pipelines,omitempty"`
	// UI — заготовка окна «Новый конвейер → Примеры» (как есть, её читает
	// веб-интерфейс).
	UI json.RawMessage `json:"ui,omitempty"`
}

var (
	keyRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	fileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)*$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	verRe  = regexp.MustCompile(`^\d{1,4}\.\d{1,4}\.\d{1,6}$`)
)

// SafePath — путь файла каталога: относительный, без «..» и странных
// знаков (он становится путём на диске хаба и в адресе скачивания).
func SafePath(p string) bool {
	return fileRe.MatchString(p) && !strings.Contains(p, "..") && path.Clean(p) == p && len(p) <= 200
}

// Validate проверяет index.json целиком, до того как хаб тронет хоть один
// файл: ключи, пути, отпечатки, версии.
func (ix *Index) Validate() error {
	if ix.Format != Format {
		return fmt.Errorf("catalog format %d, this nkt reads %d", ix.Format, Format)
	}
	seen := map[string]bool{}
	files := func(owner string, m map[string]FileRef) error {
		for p, f := range m {
			if !SafePath(p) || !shaRe.MatchString(f.SHA256) || f.Size < 0 || f.Size > MaxFile {
				return fmt.Errorf("%s: bad file %q", owner, p)
			}
		}
		return nil
	}
	for _, p := range ix.Plugins {
		if !keyRe.MatchString(p.Key) || seen["p:"+p.Key] || !verRe.MatchString(p.Version) || (p.MinNKT != "" && !verRe.MatchString(p.MinNKT)) {
			return fmt.Errorf("plugin %q: bad key or version", p.Key)
		}
		seen["p:"+p.Key] = true
		if err := files(p.Key, p.Files); err != nil {
			return err
		}
		if _, ok := p.Files["deploy/"+p.File]; !ok || !SafePath("deploy/"+p.File) {
			return fmt.Errorf("plugin %s: no file %q", p.Key, p.File)
		}
		for _, list := range p.Screens {
			for _, s := range list {
				if _, ok := p.Files[s]; !ok {
					return fmt.Errorf("plugin %s: screen %q not in files", p.Key, s)
				}
			}
		}
		for _, r := range p.Readme {
			if _, ok := p.Files[r]; !ok {
				return fmt.Errorf("plugin %s: readme %q not in files", p.Key, r)
			}
		}
	}
	for _, e := range ix.Examples {
		if !keyRe.MatchString(e.Key) || seen["e:"+e.Key] {
			return fmt.Errorf("example %q: bad key", e.Key)
		}
		seen["e:"+e.Key] = true
		if err := files(e.Key, e.Files); err != nil {
			return err
		}
		for _, f := range e.Pipelines {
			if _, ok := e.Files[f]; !ok {
				return fmt.Errorf("example %s: pipeline %q not in files", e.Key, f)
			}
		}
	}
	return nil
}

// Пределы: каталог — небольшой текст и скриншоты.
const (
	MaxIndex = 4 << 20
	MaxFile  = 8 << 20
	MaxTotal = 128 << 20
)

// --- текущий каталог хаба ---------------------------------------------------

// Snapshot — проверенный каталог в памяти: index.json и содержимое файлов.
type Snapshot struct {
	Index Index
	// Source — откуда (адрес каталога или «upload»), FetchedAt — когда.
	Source    string
	FetchedAt string
	files     map[string][]byte // "<key>/<путь>" → содержимое
}

// File — файл каталога: key — плагин или пример, rel — путь из Files.
func (s *Snapshot) File(key, rel string) ([]byte, bool) {
	if s == nil {
		return nil, false
	}
	b, ok := s.files[key+"/"+rel]
	return b, ok
}

var current atomic.Pointer[Snapshot]

// Current — каталог, с которым работает хаб (nil — ещё не загружен).
func Current() *Snapshot { return current.Load() }

// SetCurrent подменяет каталог (после проверки — Fetch, Import, Load).
func SetCurrent(s *Snapshot) { current.Store(s) }

// PluginPackage — плагин каталога вместе с файлом плагина.
type PluginPackage struct {
	Plugin
	// Content — сам файл плагина (не в JSON-ответах).
	Content []byte `json:"-"`
}

// PluginPackages — плагины текущего каталога и свои плагины хаба (ключ
// «local:<имя>»), по ключу.
func PluginPackages() []PluginPackage {
	out := localPackages()
	if s := Current(); s != nil {
		for _, p := range s.Index.Plugins {
			content, ok := s.File(p.Key, "deploy/"+p.File)
			if !ok {
				continue
			}
			out = append(out, PluginPackage{Plugin: p, Content: content})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Pipeline — пример конвейера: ключ (каталог и вариант), путь в
// репозитории, заголовок README на языке запроса и описание целиком.
type Pipeline struct {
	Key     string `json:"key"`
	File    string `json:"file"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Pipelines — все примеры конвейеров; lang — язык заголовков (ru, en).
func Pipelines(lang string) []Pipeline {
	s := Current()
	if s == nil {
		return nil
	}
	var out []Pipeline
	for _, e := range s.Index.Examples {
		for _, f := range e.Pipelines {
			raw, ok := s.File(e.Key, f)
			if !ok {
				continue
			}
			key, head := e.Key, Pick(e.Title, lang)
			if head == "" {
				head = e.Key
			}
			base := path.Base(f)
			if v := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(base, ".yaml"), "pipeline"), "."); v != "" {
				key += "-" + v // pipeline.fly.yaml → forgejo-fly
				head += " (" + v + ")"
			}
			out = append(out, Pipeline{Key: key, File: "examples/" + e.Key + "/" + f, Title: head, Content: string(raw)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Pick — значение на языке lang, иначе русское.
func Pick(m map[string]string, lang string) string {
	if v := m[lang]; v != "" {
		return v
	}
	return m["ru"]
}

// NewerOrEqual — версия a не меньше b (X.Y.Z; пустая b — любая).
func NewerOrEqual(a, b string) bool {
	if b == "" {
		return true
	}
	pa, pb := versionParts(a), versionParts(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return true
}

func versionParts(v string) [3]int {
	var out [3]int
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	for i, p := range strings.SplitN(v, ".", 3) {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
