package hub

import (
	"testing"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// К какому registry относится ключ конвейера: registry: описания, а без
// него — единственный registry кроме Docker Hub; два своих — не угадывать.
func TestRegistryFor(t *testing.T) {
	key, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := secretbox.Encrypt(key, []byte("piqab:ghp_x"))
	s := &Server{hub: &Manager{key: key}}
	pl := store.Pipeline{RegistryCred: enc}
	cases := []struct {
		spec   deploy.Spec
		images []string
		host   string
		ok     bool
	}{
		{deploy.Spec{}, []string{"ghcr.io/piqab/rgstr:latest", "postgres:16", "redis"}, "ghcr.io", true},
		{deploy.Spec{}, []string{"org/private:1", "postgres:16"}, "docker.io", true},
		{deploy.Spec{}, []string{"ghcr.io/a/b", "registry.example.com/c"}, "", false},
		{deploy.Spec{Registry: "registry.example.com/c"}, []string{"ghcr.io/a/b", "registry.example.com/c"}, "registry.example.com", true},
	}
	for _, c := range cases {
		reg, ok := s.registryFor(pl, c.spec, c.images)
		if ok != c.ok || reg.Host != c.host || (ok && reg.cred() != "piqab:ghp_x") {
			t.Errorf("%v %v: %+v %v", c.spec.Registry, c.images, reg, ok)
		}
	}
	if _, ok := s.registryFor(store.Pipeline{}, deploy.Spec{}, []string{"ghcr.io/a/b"}); ok {
		t.Error("без ключа")
	}
}
