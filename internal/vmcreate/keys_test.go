package vmcreate

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// Ключ, который nkt заводит сам, должен и разбираться как ключ, и
// подходить к тому, что уходит в машину: иначе оператор получит на руки
// бумажку, которой некуда приложиться.
func TestGenerateKeyPair(t *testing.T) {
	priv, authorized, err := GenerateKeyPair("web-01")
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(priv))
	if err != nil {
		t.Fatalf("приватный ключ не разбирается: %v", err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorized))
	if err != nil {
		t.Fatalf("публичный ключ не разбирается: %v", err)
	}
	if string(pub.Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Error("половины пары не совпадают")
	}
	if !strings.Contains(authorized, "nkt-web-01") {
		t.Errorf("в ключе нет пометки, откуда он: %q", authorized)
	}

	// Описание с таким ключом должно проходить проверку — иначе
	// сгенерированный ключ отвергался бы собственной формой.
	s := validSpec()
	s.SSHKey = authorized
	if err := s.Validate(); err != nil {
		t.Errorf("сгенерированный ключ не принят проверкой: %v", err)
	}
}
