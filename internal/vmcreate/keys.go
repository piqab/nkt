package vmcreate

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"github.com/piqab/nkt/internal/msgs"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Облачный образ приходит без пароля: единственный вход в машину — ключ.
// Требовать его от оператора неудобно (ключа под рукой может не быть), а
// создавать машину, в которую нельзя войти, — бессмысленно. Поэтому пару
// заводит сам nkt: публичная половина уходит в машину при первом
// запуске, приватная показывается один раз тому, кто нажал кнопку.
//
// Один раз — потому что хранить её негде: в базе она стала бы ключом от
// всех созданных машин, лежащим рядом с ними.

// GenerateKeyPair создаёт ed25519-пару для новой машины.
func GenerateKeyPair(name string) (privatePEM, authorizedKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", msgs.Errorf("control.generatingKey", err)
	}
	comment := "nkt-" + name
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", msgs.Errorf("hub.serializingPrivateKey", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", msgs.Errorf("hub.serializingPublicKey", err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment
	return string(pem.EncodeToMemory(block)), line, nil
}
