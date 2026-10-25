// Package examples — описания конвейеров из примеров (pipeline*.yaml):
// хаб отдаёт их агентам MCP (list_pipeline_examples) как заготовки.
package examples

import (
	"embed"
	"encoding/json"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed */pipeline*.yaml */README.md */README.en.md */deploy/nkt-plugin.json */deploy/nkt-plugin.py
var files embed.FS

// Pipeline — пример: ключ (каталог и вариант), путь в репозитории,
// заголовок README на языке запроса и описание целиком.
type Pipeline struct {
	Key     string `json:"key"`
	File    string `json:"file"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

// Pipelines — все примеры по ключу; lang — язык заголовков (ru, en).
func Pipelines(lang string) []Pipeline {
	var out []Pipeline
	_ = fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		dir, base := path.Split(p)
		if !strings.HasPrefix(base, "pipeline") {
			return nil
		}
		raw, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		key := strings.TrimSuffix(dir, "/")
		head := readmeTitle(dir, lang)
		if v := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(base, ".yaml"), "pipeline"), "."); v != "" {
			key += "-" + v // pipeline.fly.yaml → forgejo-fly
			head += " (" + v + ")"
		}
		out = append(out, Pipeline{Key: key, File: "examples/" + p, Title: head, Content: string(raw)})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// readmeTitle — заголовок README каталога (en — README.en.md).
func readmeTitle(dir, lang string) string {
	name := "README.md"
	if lang == "en" {
		name = "README.en.md"
	}
	raw, err := files.ReadFile(dir + name)
	if err != nil {
		return strings.TrimSuffix(dir, "/")
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	return strings.TrimSpace(strings.TrimPrefix(first, "#"))
}

// PluginPackage — плагин, который хаб ставит на хост службой без Docker
// (deploy/nkt-plugin.json рядом с файлом плагина).
type PluginPackage struct {
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Title       map[string]string `json:"title"`
	Description map[string]string `json:"description"`
	Run         string            `json:"run"`
	File        string            `json:"file"`
	Port        int               `json:"port"`
	// Docker — плагину нужен доступ к Docker на хосте (служба в группе
	// docker — это права root): хаб показывает это до установки.
	Docker   bool            `json:"docker,omitempty"`
	Settings []PluginSetting `json:"settings"`
	// Content — сам файл плагина (не в JSON-ответах).
	Content []byte `json:"-"`
}

// PluginSetting — поле формы установки: переменная окружения плагина.
type PluginSetting struct {
	Env      string            `json:"env"`
	Title    map[string]string `json:"title"`
	Hint     map[string]string `json:"hint,omitempty"`
	Default  string            `json:"default,omitempty"`
	Secret   bool              `json:"secret,omitempty"`
	Required bool              `json:"required,omitempty"`
}

// PluginPackages — встроенный каталог плагинов для установки без Docker.
func PluginPackages() []PluginPackage {
	var out []PluginPackage
	matches, _ := fs.Glob(files, "*/deploy/nkt-plugin.json")
	for _, m := range matches {
		raw, err := files.ReadFile(m)
		if err != nil {
			continue
		}
		var p PluginPackage
		if json.Unmarshal(raw, &p) != nil || p.File == "" {
			continue
		}
		dir := path.Dir(m)
		if p.Content, err = files.ReadFile(dir + "/" + p.File); err != nil {
			continue
		}
		p.Key = strings.SplitN(m, "/", 2)[0]
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
