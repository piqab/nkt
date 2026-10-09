package hub

import (
	"encoding/json"
	"strings"
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

// Образ задан переменной .env — ключ подбирается по раскрытому имени;
// ключ, который не подходит ни одному образу, назван.
func TestRegistryKeysForEnvImage(t *testing.T) {
	compose := "services:\n  station:\n    image: ${VIDEOLOG_STATION_IMAGE:-ghcr.io/piqab/videolog-station:latest}\n  db:\n    image: postgres:16\n"
	env := "VIDEOLOG_STATION_IMAGE=\"rgstr.example.com/test/videolog/videolog-station:latest\"\nVIDEOLOG_PULL_POLICY=always\n"
	images := deploy.ComposeImagesEnv(compose, envValues(&env))
	keys := []registryKey{{Host: "rgstr.example.com", User: "robot", Token: "t"}, {Host: "harbor.typo.example", User: "u", Token: "t"}}
	if k, ok := registryKeyFor(keys, deploy.RegistryHost(images[1]), ""); !ok || k.User != "robot" {
		t.Fatalf("%v %v", images, k)
	}
	if got := unusedRegistryKeys(keys, deploy.Spec{}, images); len(got) != 1 || got[0] != "harbor.typo.example" {
		t.Fatal(got)
	}
	if got := unusedRegistryKeys(keys, deploy.Spec{Registry: "harbor.typo.example/app"}, images); len(got) != 0 {
		t.Fatal("ключ для registry: — не лишний", got)
	}
	if got := imageRegistries(images); strings.Join(got, ",") != "docker.io,rgstr.example.com" {
		t.Fatal(got)
	}
}
