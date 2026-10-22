package svcdesc

import (
	"encoding/json"
	"testing"
)

// Примеры RFC 9535 (раздел 1.5, книжный магазин) и фильтры.
const store = `{ "store": {
    "book": [
      { "category": "reference", "author": "Nigel Rees", "title": "Sayings of the Century", "price": 8.95 },
      { "category": "fiction", "author": "Evelyn Waugh", "title": "Sword of Honour", "price": 12.99 },
      { "category": "fiction", "author": "Herman Melville", "title": "Moby Dick", "isbn": "0-553-21311-3", "price": 8.99 },
      { "category": "fiction", "author": "J. R. R. Tolkien", "title": "The Lord of the Rings", "isbn": "0-395-19395-8", "price": 22.99 }
    ],
    "bicycle": { "color": "red", "price": 399 }
  } }`

func evalJSON(t *testing.T, doc, path string) []any {
	t.Helper()
	root, err := Decode(FormatJSON, []byte(doc), FormatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := CompilePath(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var out []any
	for _, n := range p.Eval(root, root) {
		out = append(out, Plain(n.Value))
	}
	return out
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestJSONPathRFCExamples(t *testing.T) {
	cases := []struct{ path, want string }{
		{"$.store.book[*].author", `["Nigel Rees","Evelyn Waugh","Herman Melville","J. R. R. Tolkien"]`},
		{"$..author", `["Nigel Rees","Evelyn Waugh","Herman Melville","J. R. R. Tolkien"]`},
		{"$.store..price", `[8.95,12.99,8.99,22.99,399]`},
		{"$..book[2].title", `["Moby Dick"]`},
		{"$..book[-1].title", `["The Lord of the Rings"]`},
		{"$..book[0,1].title", `["Sayings of the Century","Sword of Honour"]`},
		{"$..book[:2].title", `["Sayings of the Century","Sword of Honour"]`},
		{"$..book[::-1][0].title", `null`},
		{"$..book[?@.isbn].title", `["Moby Dick","The Lord of the Rings"]`},
		{"$..book[?@.price<10].title", `["Sayings of the Century","Moby Dick"]`},
		{"$..book[?@.price<10 && @.category=='fiction'].title", `["Moby Dick"]`},
		{"$..book[?!@.isbn].title", `["Sayings of the Century","Sword of Honour"]`},
		{"$..book[?match(@.author, 'J.*')].price", `[22.99]`},
		{"$..book[?search(@.title, 'of')].price", `[8.95,12.99,22.99]`},
		{"$..book[?length(@.title) > 15].price", `[8.95,22.99]`},
		{"$.store.bicycle['color']", `["red"]`},
		{"$.store.*.color", `["red"]`},
		{"$['store']['bicycle'].price", `[399]`},
		{"$..book[?@.price == $.store.book[0].price].title", `["Sayings of the Century"]`},
		{"$.store.book[1:3].author", `["Evelyn Waugh","Herman Melville"]`},
		{"$.store.book[::2].author", `["Nigel Rees","Herman Melville"]`},
		{"$.store.book[::-1].author", `["J. R. R. Tolkien","Herman Melville","Evelyn Waugh","Nigel Rees"]`},
	}
	for _, c := range cases {
		if got := asJSON(evalJSON(t, store, c.path)); got != c.want {
			t.Errorf("%s: %s, want %s", c.path, got, c.want)
		}
	}
	if n := len(evalJSON(t, store, "$..*")); n != 27 {
		t.Errorf("$..*: %d nodes", n)
	}
}

// Отступление ради конфигов: строка-число и слова-логические.
func TestJSONPathLenientCompare(t *testing.T) {
	doc := `{"a":{"port":"6379","on":"yes"},"b":{"port":"80","on":"no"}}`
	if got := asJSON(evalJSON(t, doc, "$[?@.port == 6379].on")); got != `["yes"]` {
		t.Errorf("number vs string: %s", got)
	}
	if got := asJSON(evalJSON(t, doc, "$[?@.on == true].port")); got != `["6379"]` {
		t.Errorf("bool vs word: %s", got)
	}
	if got := asJSON(evalJSON(t, doc, "$[?@.port > 100].port")); got != `["6379"]` {
		t.Errorf("order: %s", got)
	}
}

func TestJSONPathErrors(t *testing.T) {
	for _, bad := range []string{"store", "$.", "$[", "$[?@.a ==]", "$.a)", "$..", "$['x'", "$[?foo(@)]", "$[?match(@.a)]"} {
		if _, err := CompilePath(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestExprPipes(t *testing.T) {
	root, _ := Decode(FormatYAML, []byte("ep:\n  web:\n    address: \":8080\"\n  tls:\n    address: \"10.0.0.5:8443\"\nurl: https://example.com/x\nlist: a, b ,c\n"), FormatOptions{})
	ctx := Ctx{Root: root, Item: Node{Key: "web", Value: root.(*Obj).Values["ep"].(*Obj).Values["web"]}, File: "/etc/x.yml"}
	cases := []struct {
		expr string
		want string
	}{
		{"@.address | port", `[8080]`},
		{"@.address | host", `[""]`},
		{"$.ep.tls.address | host", `["10.0.0.5"]`},
		{"$.url | port", `[443]`},
		{"$.url | host | upper", `["EXAMPLE.COM"]`},
		{"$.url | scheme", `["https"]`},
		{"$.list | split(,)", `["a","b","c"]`},
		{"$.list | split(',') | join(+)", `["a+b+c"]`},
		{"$.missing | default(none)", `["none"]`},
		{"@key", `["web"]`},
		{"@file", `["/etc/x.yml"]`},
		{"$.ep.* | count", `[2]`},
		{"$.ep | keys", `["web","tls"]`},
		{"$.ep..address | last | port", `[8443]`},
		{"tcp", `["tcp"]`},
		{"$.list | split(,) | nth(1)", `["b"]`},
		{"$.list | split(,) | nth(-1)", `["c"]`},
		{"':53/udp' | port", `[53]`},
		{"'10.0.0.1:53/udp' | host", `["10.0.0.1"]`},
		{"'a | b'", `["a | b"]`},
	}
	for _, c := range cases {
		e, err := CompileExpr(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := asJSON(e.Values(ctx)); got != c.want {
			t.Errorf("%s: %s, want %s", c.expr, got, c.want)
		}
	}
	if _, err := CompileExpr("@.a | nope"); err == nil {
		t.Error("unknown pipe: want error")
	}
	if _, err := CompileExpr("@.a | split"); err == nil {
		t.Error("split without args: want error")
	}
}
