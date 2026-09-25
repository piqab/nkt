package vmcreate

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword(t *testing.T) {
	h, err := HashPassword("Very$ecret1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$2a$12$") || bcrypt.CompareHashAndPassword([]byte(h), []byte("Very$ecret1")) != nil {
		t.Fatalf("%s", h)
	}
	if bcrypt.CompareHashAndPassword([]byte(h), []byte("nope")) == nil {
		t.Fatal("чужой пароль подошёл")
	}
}
