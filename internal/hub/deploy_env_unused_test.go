package hub

import (
	"strings"
	"testing"
)

func TestEnvUnused(t *testing.T) {
	env := "DB_PASS=x\nAPI_KEY=y\nUNUSED=z\nCOMPOSE_PROJECT_NAME=p\n# COMMENTED=1\n"
	compose := "services:\n  app:\n    image: a\n    environment:\n      DB_PASS: ${DB_PASS}\n      KEY: $API_KEY\n"
	if got := strings.Join(envUnused(env, compose), ","); got != "UNUSED" {
		t.Fatalf("неиспользуемые: %q", got)
	}
	for _, ef := range []string{
		"    env_file: .env\n",
		"    env_file: [\".env\"]\n",
		"    env_file:\n      - .env\n",
		"    env_file:\n      - path: ./.env\n        required: false\n",
	} {
		if got := envUnused(env, "services:\n  app:\n    image: a\n"+ef); got != nil {
			t.Errorf("env_file %q — всё используется, а не %v", ef, got)
		}
	}
	if got := envUnused(env, "services:\n  app:\n    env_file: app.env\n"); len(got) != 3 {
		t.Errorf("app.env — не .env: %v", got)
	}
}
