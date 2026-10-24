// Package examples — описания конвейеров из примеров (pipeline*.yaml):
// хаб отдаёт их агентам MCP (list_pipeline_examples) как заготовки.
package examples

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed */pipeline*.yaml */README.md */README.en.md
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
