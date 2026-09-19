package msgs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Сообщение в хранимом виде: ключ каталога и аргументы с типами — чтобы
// строку журнала, заголовок или ошибку задания можно было отрисовать на
// языке того, кто её читает, а не того, кто запустил задание. Текст при
// записи тоже сохраняется (на языке автора) — для старых клиентов, CLI и
// строк без ключа (сырой вывод инструментов).
//
// Аргументы кодируются с типом: {"s":…} строка, {"i":…} целое, {"f":…}
// дробное, {"b":…} логическое, {"e":{…}} вложенное сообщение (msgs.Err —
// локализуется вместе с внешним), остальное — строкой через %v. Целые и
// дробные различаются нарочно: JSON превратил бы 3 в 3.0, и «%d» сломался.

type encodedArg struct {
	S *string      `json:"s,omitempty"`
	I *int64       `json:"i,omitempty"`
	F *float64     `json:"f,omitempty"`
	B *bool        `json:"b,omitempty"`
	E *encodedMsg  `json:"e,omitempty"`
	L []encodedArg `json:"l,omitempty"`
}

// List — аргумент-перечень: элементы (строки или *Err) локализуются
// каждый и склеиваются через «; » — «проверок не пройдено: 2 — a; b».
type List []any

// In — перечень на языке lang.
func (l List) In(lang Lang) string {
	parts := make([]string, 0, len(l))
	for _, a := range l {
		switch v := a.(type) {
		case *Err:
			parts = append(parts, v.In(lang))
		case error:
			parts = append(parts, Localize(lang, v))
		default:
			parts = append(parts, fmt.Sprint(v))
		}
	}
	return strings.Join(parts, "; ")
}

// String — на языке по умолчанию (для fmt в Err.Error()).
func (l List) String() string { return l.In(DefaultLang) }

type encodedMsg struct {
	Key  string       `json:"k"`
	Args []encodedArg `json:"a,omitempty"`
}

// EncodeArgs — аргументы в JSON для хранения рядом с ключом.
func EncodeArgs(args []any) string {
	if len(args) == 0 {
		return ""
	}
	raw, err := json.Marshal(encodeArgs(args))
	if err != nil {
		return ""
	}
	return string(raw)
}

func encodeArgs(args []any) []encodedArg {
	out := make([]encodedArg, 0, len(args))
	for _, a := range args {
		out = append(out, encodeArg(a))
	}
	return out
}

func encodeArg(a any) encodedArg {
	switch v := a.(type) {
	case string:
		return encodedArg{S: &v}
	case bool:
		return encodedArg{B: &v}
	case int:
		i := int64(v)
		return encodedArg{I: &i}
	case int64:
		return encodedArg{I: &v}
	case int32:
		i := int64(v)
		return encodedArg{I: &i}
	case uint:
		i := int64(v)
		return encodedArg{I: &i}
	case uint64:
		i := int64(v)
		return encodedArg{I: &i}
	case float64:
		return encodedArg{F: &v}
	case float32:
		f := float64(v)
		return encodedArg{F: &f}
	case *Err:
		return encodedArg{E: &encodedMsg{Key: v.Key, Args: encodeArgs(v.Args)}}
	case List:
		enc := encodeArgs([]any(v))
		if enc == nil {
			enc = []encodedArg{}
		}
		return encodedArg{L: enc}
	case error:
		var e *Err
		if asErr(v, &e) && e == v {
			return encodedArg{E: &encodedMsg{Key: e.Key, Args: encodeArgs(e.Args)}}
		}
		s := v.Error()
		return encodedArg{S: &s}
	default:
		s := fmt.Sprint(a)
		return encodedArg{S: &s}
	}
}

// DecodeArgs восстанавливает аргументы из EncodeArgs; пустая строка —
// без аргументов.
func DecodeArgs(raw string) []any {
	if raw == "" {
		return nil
	}
	var enc []encodedArg
	if err := json.Unmarshal([]byte(raw), &enc); err != nil {
		return nil
	}
	return decodeArgs(enc)
}

func decodeArgs(enc []encodedArg) []any {
	out := make([]any, 0, len(enc))
	for _, a := range enc {
		switch {
		case a.S != nil:
			out = append(out, *a.S)
		case a.I != nil:
			out = append(out, *a.I)
		case a.F != nil:
			out = append(out, *a.F)
		case a.B != nil:
			out = append(out, *a.B)
		case a.E != nil:
			out = append(out, &Err{Key: a.E.Key, Args: decodeArgs(a.E.Args)})
		case a.L != nil:
			out = append(out, List(decodeArgs(a.L)))
		default:
			out = append(out, "")
		}
	}
	return out
}

// Render — текст по ключу и хранимым аргументам на языке lang; без
// ключа — fallback как есть.
func Render(lang Lang, key, rawArgs, fallback string) string {
	if key == "" {
		return fallback
	}
	args := DecodeArgs(rawArgs)
	for i, a := range args {
		switch v := a.(type) {
		case *Err:
			args[i] = v.In(lang)
		case List:
			args[i] = v.In(lang)
		}
	}
	return T(lang, key, args...)
}

// ErrParts — ключ и аргументы ошибки для хранения (*Err, в том числе
// обёрнутый); у прочих ошибок ключа нет.
func ErrParts(err error) (key, rawArgs string) {
	var e *Err
	if err == nil || !asErr(err, &e) || e != err {
		return "", ""
	}
	return e.Key, EncodeArgs(e.Args)
}

func asErr(err error, target **Err) bool { return errors.As(err, target) }
