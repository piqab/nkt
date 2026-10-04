package api

import "testing"

func TestEnvOrigins(t *testing.T) {
	got := envOrigins([]string{"PATH=/bin", "TZ=UTC", "APP_SECRET=s"}, []string{"PATH=/bin", "TZ=Europe/Berlin"}, false)
	want := []struct{ name, origin string }{{"APP_SECRET", "container"}, {"TZ", "override"}, {"PATH", "image"}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, w := range want {
		if got[i].Name != w.name || got[i].Origin != w.origin || got[i].Value != "" || !got[i].Masked {
			t.Errorf("%d: %+v, want %s/%s замазано", i, got[i], w.name, w.origin)
		}
	}
	if r := envOrigins([]string{"A=1"}, nil, true); r[0].Value != "1" || r[0].Masked {
		t.Errorf("раскрыто: %+v", r)
	}
	if string(maskRawEnv([]byte(`{"Config":{"Env":["A=secret"]}}`))) != `{"Config":{"Env":["A=••••••"]}}` {
		t.Error("JSON не замазан")
	}
}
