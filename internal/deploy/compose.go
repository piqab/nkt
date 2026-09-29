package deploy

import (
	"sort"

	"gopkg.in/yaml.v3"
)

// BuildOnlyServices — сервисы compose-файла, которые собираются из
// исходников (build: без image:). На хосте их собрать не из чего: стек
// едет туда без контекста сборки, а выкладка берёт только готовые образы.
// Нечитаемый YAML — не наша забота: его отвергнет «compose config».
func BuildOnlyServices(text string) []string {
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(text), &doc) != nil {
		return nil
	}
	var out []string
	for name, svc := range doc.Services {
		_, build := svc["build"]
		image, _ := svc["image"].(string)
		if build && image == "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
