package hub

import (
	"reflect"
	"testing"
)

// «Переменные без значения» из хеша пароля в compose или .env — где они:
// файл и строка, без самих значений. Настоящая переменная ${VAR} или $VAR
// одна в слове, и экранированный $$ сюда не попадают.
func TestDollarInValues(t *testing.T) {
	src := dollarSource{
		files: map[string]string{
			"docker-compose.yml": "services:\n  web:\n    environment:\n      - AUTH=admin:$2y$10$j9TnL4abc\n      - URL=$BASE_URL/x\n      - OK=admin:$$2y$$10$$zzz\n",
		},
		env: "HASH=\"$apr1$tVzSoQbo06cJtOwEr6YkH$q\"\nPLAIN=$OTHER\n",
	}
	got := dollarInValues([]string{"y", "j9TnL4abc", "tVzSoQbo06cJtOwEr6YkH", "BASE_URL", "OTHER", "zzz"}, src)
	want := []string{"docker-compose.yml:4", ".env:1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := dollarInValues([]string{"BASE_URL"}, src); len(got) != 0 {
		t.Errorf("настоящая переменная: %v", got)
	}
}
