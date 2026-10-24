package examples

import "testing"

func TestPipelines(t *testing.T) {
	list := Pipelines("en")
	keys := map[string]bool{}
	for _, p := range list {
		keys[p.Key] = true
		if p.Title == "" || p.Content == "" {
			t.Errorf("%s: no title or content", p.Key)
		}
	}
	for _, p := range list {
		for _, r := range p.Title {
			if r >= 'а' && r <= 'я' {
				t.Errorf("%s: Russian title %q", p.Key, p.Title)
				break
			}
		}
	}
	for _, k := range []string{"forgejo", "forgejo-fly", "forgejo-runner", "technitium", "technitium-secondary"} {
		if !keys[k] {
			t.Errorf("no example %s in %v", k, keys)
		}
	}
}
