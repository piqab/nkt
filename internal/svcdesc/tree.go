package svcdesc

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Дерево данных, над которым работают пути: каждый формат (yaml, json,
// ini, xml…) читается в одно и то же дерево. Объект — упорядоченный:
// порядок ключей из файла сохраняется, иначе порядок адресов и находок
// плавал бы от скана к скану. Значения: *Obj, []any, string, float64,
// bool, nil.

// Obj — объект с порядком ключей.
type Obj struct {
	Keys   []string
	Values map[string]any
}

// NewObj — пустой объект.
func NewObj() *Obj { return &Obj{Values: map[string]any{}} }

// Set задаёт значение (новый ключ — в конец).
func (o *Obj) Set(k string, v any) {
	if _, ok := o.Values[k]; !ok {
		o.Keys = append(o.Keys, k)
	}
	o.Values[k] = v
}

// Add — повторяющийся ключ становится списком (ini, «строки», xml).
func (o *Obj) Add(k string, v any) {
	cur, ok := o.Values[k]
	if !ok {
		o.Set(k, v)
		return
	}
	if l, isList := cur.(multi); isList {
		o.Values[k] = append(l, v)
		return
	}
	o.Values[k] = multi{cur, v}
}

// Get — значение ключа.
func (o *Obj) Get(k string) (any, bool) {
	v, ok := o.Values[k]
	return v, ok
}

// multi — список, собранный из повторов ключа; после чтения превращается
// в обычный []any (finish).
type multi []any

// finish заменяет multi на []any по всему дереву.
func finish(v any) any {
	switch t := v.(type) {
	case *Obj:
		for _, k := range t.Keys {
			t.Values[k] = finish(t.Values[k])
		}
		return t
	case multi:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = finish(x)
		}
		return out
	case []any:
		for i := range t {
			t[i] = finish(t[i])
		}
		return t
	}
	return v
}

// fromPlain — map/срез из декодеров (toml) в дерево; ключи — по алфавиту.
func fromPlain(v any) any {
	switch t := v.(type) {
	case map[string]any:
		o := NewObj()
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			o.Set(k, fromPlain(t[k]))
		}
		return o
	case []map[string]any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = fromPlain(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = fromPlain(x)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case float32:
		return float64(t)
	case fmt.Stringer:
		return t.String()
	}
	return v
}

// Plain — дерево в обычные map/срезы (для JSON-вывода журнала разбора).
func Plain(v any) any {
	switch t := v.(type) {
	case *Obj:
		m := make(map[string]any, len(t.Keys))
		for _, k := range t.Keys {
			m[k] = Plain(t.Values[k])
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Plain(x)
		}
		return out
	}
	return v
}

// Text — значение строкой: числа без хвостовых нулей, nil — пусто.
func Text(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case *Obj, []any:
		return fmt.Sprint(Plain(t))
	}
	return fmt.Sprint(v)
}

// Truthy — значение как условие: пустое, 0, false, «no»/«off» — ложь.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "", "0", "false", "no", "off":
			return false
		}
		return true
	case []any:
		return len(t) > 0
	case *Obj:
		return len(t.Keys) > 0
	}
	return true
}
