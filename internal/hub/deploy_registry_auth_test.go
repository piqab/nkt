package hub

import (
	"encoding/json"
	"testing"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Ключи registry списком: каждому образу — ключ его registry, публичным —
// никакого; прежний одиночный ключ без адреса — по старому правилу
// (registry: описания или единственный registry стека кроме Docker Hub).
func TestRegistriesFor(t *testing.T) {
	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{hub: &Manager{key: key}}
	list, _ := json.Marshal([]registryKey{
		{Host: "harbor.example.com", User: "robot$team+deploy", Token: "h", CA: "-----BEGIN CERTIFICATE-----"},
		{Host: "ghcr.io", User: "piqab", Token: "g"},
	})
	enc, _ := secretbox.Encrypt(key, list)
	pl := store.Pipeline{RegistriesEnc: enc}
	got := s.registriesFor(pl, deploy.Spec{}, []string{"harbor.example.com/team/app:1", "ghcr.io/piqab/rgstr", "postgres:16"})
	if len(got) != 2 || got["harbor.example.com"].Token != "h" || got["ghcr.io"].Token != "g" || got["harbor.example.com"].CA == "" {
		t.Fatalf("список: %+v", got)
	}
	if _, ok := got["docker.io"]; ok {
		t.Error("публичному образу — ключ")
	}

	legacy, _ := secretbox.Encrypt(key, []byte("piqab:ghp_x"))
	old := store.Pipeline{RegistryCred: legacy}
	if got := s.registriesFor(old, deploy.Spec{}, []string{"ghcr.io/piqab/rgstr", "postgres:16"}); got["ghcr.io"].Token != "ghp_x" {
		t.Errorf("прежний ключ: %+v", got)
	}
	two := []string{"ghcr.io/a/b", "registry.example.com/c"}
	if got := s.registriesFor(old, deploy.Spec{}, two); len(got) != 0 || !s.unboundKeyLost(old, deploy.Spec{}, two) {
		t.Errorf("два своих registry без registry: — %+v", got)
	}
	if got := s.registriesFor(old, deploy.Spec{Registry: "registry.example.com/c"}, two); got["registry.example.com"].Token != "ghp_x" {
		t.Errorf("registry: описания: %+v", got)
	}
}
