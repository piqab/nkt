package msgs

import "testing"

func TestEncodeDecodeArgs(t *testing.T) {
	inner := Errorf("hub.hostFound", "boom")
	args := []any{"web-1", 3, int64(7), 2.5, true, inner}
	raw := EncodeArgs(args)
	got := DecodeArgs(raw)
	if len(got) != len(args) {
		t.Fatalf("decoded %d args: %v", len(got), got)
	}
	if got[0] != "web-1" || got[1] != int64(3) || got[2] != int64(7) || got[3] != 2.5 || got[4] != true {
		t.Errorf("args: %#v", got)
	}
	if e, ok := got[5].(*Err); !ok || e.Key != "hub.hostFound" {
		t.Errorf("nested error: %#v", got[5])
	}
	// %d с int64 из JSON форматируется как число, а не как «%!d(float64=3)».
	if s := Render(EN, "hub.preflightFailed", EncodeArgs([]any{2}), ""); s != T(EN, "hub.preflightFailed", 2) || s == "" {
		t.Errorf("render: %q", s)
	}
	if s := Render(RU, "", "", "raw text"); s != "raw text" {
		t.Errorf("fallback: %q", s)
	}
	k, a := ErrParts(Errorf("hub.preflightFailed", 5))
	if k != "hub.preflightFailed" || a == "" {
		t.Errorf("ErrParts: %q %q", k, a)
	}
}

// Перечень (msgs.List) переживает хранение и локализуется поэлементно.
func TestEncodeListLocalized(t *testing.T) {
	raw := EncodeArgs([]any{2, List{&Err{Key: "hub.clusterImageMissing", Args: []any{"a.qcow2"}}, "raw"}})
	got := Render(EN, "hub.preflightFailedList", raw, "")
	want := T(EN, "hub.preflightFailedList", 2, T(EN, "hub.clusterImageMissing", "a.qcow2")+"; raw")
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	e := &Err{Key: "hub.preflightFailedList", Args: []any{1, List{&Err{Key: "hub.clusterImageMissing", Args: []any{"a.qcow2"}}}}}
	if e.In(EN) != T(EN, "hub.preflightFailedList", 1, T(EN, "hub.clusterImageMissing", "a.qcow2")) {
		t.Errorf("Err.In с List = %q", e.In(EN))
	}
}
