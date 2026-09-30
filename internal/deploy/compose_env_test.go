package deploy

import (
	"os"
	"strings"
	"testing"
)

// compose.env_keys на compose-файле umami: секреты — ссылками на .env.
func TestEnvKeysUmami(t *testing.T) {
	src, err := os.ReadFile("testdata/umami-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string][]string{
		"umami": {"APP_SECRET", "TWO_FACTOR_ENCRYPTION_KEY", "DATABASE_URL"},
		"db":    {"POSTGRES_PASSWORD"},
	}
	out, err := OverrideServices(string(src), nil, nil, keys)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`APP_SECRET: "${APP_SECRET}"`, `DATABASE_URL: "${DATABASE_URL}"`, `POSTGRES_PASSWORD: "${POSTGRES_PASSWORD}"`, "POSTGRES_USER: umami"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in\n%s", want, out)
		}
	}
	if strings.Contains(out, "replace-me") {
		t.Fatalf("literal secret left:\n%s", out)
	}
	// Список «KEY=value» и сервис без environment.
	list := "services:\n  a:\n    image: x\n    environment:\n      - A=1\n      - B=2\n  b:\n    image: y\n"
	out, err = OverrideServices(list, nil, nil, map[string][]string{"a": {"B", "C"}, "b": {"D"}})
	if err != nil || !strings.Contains(out, `"B=${B}"`) || !strings.Contains(out, `"C=${C}"`) || !strings.Contains(out, "A=1") || !strings.Contains(out, `D: "${D}"`) {
		t.Fatalf("%v\n%s", err, out)
	}
	base := "repo: https://github.com/umami-software/umami.git\nref: master\naction: compose\ncompose:\n  file: docker-compose.yml\n  project: umami\n  hosts: [cn4]\n"
	s, err := ParseSpec(base + "  env_keys:\n    umami: [APP_SECRET, DATABASE_URL]\n    db: [POSTGRES_PASSWORD]\n")
	if err != nil || strings.Join(s.Compose.AllEnvKeys(), ",") != "APP_SECRET,DATABASE_URL,POSTGRES_PASSWORD" {
		t.Fatalf("%v %v", s.Compose.AllEnvKeys(), err)
	}
	for _, bad := range []string{"  env_keys:\n    umami: ['APP SECRET']\n", "  env_keys:\n    'a b': [X]\n"} {
		if _, err := ParseSpec(base + bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
