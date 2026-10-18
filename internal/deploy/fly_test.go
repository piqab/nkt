package deploy

import "testing"

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
