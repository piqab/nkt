package deploy

import (
	"strings"
	"testing"
)

func TestValidateFly(t *testing.T) {
	ok := []FlySpec{
		{Route: "app", Names: []string{"App.Example.com"}, TLS: "gateway", Service: "web", TargetPort: 8080},
		{Route: "mqtt", Kind: "tcp", Port: 1883, Service: "broker", TargetPort: 1883},
		{Route: "turn-relay", Kind: "udp", Port: 49152, PortTo: 65535, Service: "coturn", TargetPort: 49152},
	}
	if err := validateFly(ok); err != nil {
		t.Fatal(err)
	}
	if ok[0].Port != 443 || ok[0].Kind != "https" || ok[0].Names[0] != "app.example.com" || ok[2].Check != "none" {
		t.Fatalf("defaults: %+v %+v", ok[0], ok[2])
	}
	bad := []FlySpec{
		{Route: "x", Service: "web", TargetPort: 80},                                                              // https без имён
		{Route: "x", Names: []string{"*.example.com"}, TLS: "gateway", Service: "web", TargetPort: 80},            // маска с сертификатом на шлюзе
		{Route: "x", Kind: "tcp", Service: "web", TargetPort: 80},                                                 // tcp без порта
		{Route: "x", Kind: "udp", Port: 49152, PortTo: 65535, Service: "coturn", TargetPort: 3478},                // диапазон меняет порт
		{Route: "x", Kind: "tcp", Port: 1883, Names: []string{"a.example.com"}, Service: "web", TargetPort: 1883}, // имена у tcp
		{Route: "X Y", Names: []string{"a.example.com"}, Service: "web", TargetPort: 80},
		{Route: "x", Names: []string{"a.example.com"}, Service: "web; rm", TargetPort: 80},
	}
	for i, f := range bad {
		if validateFly([]FlySpec{f}) == nil {
			t.Errorf("%d accepted: %+v", i, f)
		}
	}
	if validateFly([]FlySpec{ok[1], ok[1]}) == nil {
		t.Error("route twice accepted")
	}
}

// Неверный идентификатор маршрута в fly: — ошибка с правилом и готовым
// вариантом; «*.» с сертификатом на шлюзе — своей ошибкой.
func TestFlyRouteIDHint(t *testing.T) {
	for in, want := range map[string]string{"Git.Example": "git-example", "_web_": "web", "--": "git-example-com"} {
		if got := SuggestFlyRouteID(in, []string{"git.example.com"}); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	f := FlySpec{Route: "Git Web", Kind: "https", Names: []string{"git.example.com"}, Service: "web", TargetPort: 3000}
	if err := f.validate(0); err == nil || !strings.Contains(err.Error(), "git-web") {
		t.Fatalf("route hint: %v", err)
	}
	f = FlySpec{Route: "web", Kind: "https", Names: []string{"*.example.com"}, TLS: "gateway", Service: "web", TargetPort: 3000}
	if err := f.validate(0); err == nil || !strings.Contains(err.Error(), "*.") {
		t.Fatalf("wildcard tls: %v", err)
	}
}
