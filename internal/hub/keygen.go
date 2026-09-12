package hub

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"github.com/piqab/nkt/internal/msgs"
	"strings"

	"golang.org/x/crypto/ssh"
)

// generateHostKeyPair creates a fresh ed25519 keypair for a hub-managed
// host — the safer default over accepting a credential from the operator:
// the private half is generated here, never leaves the hub, and the caller
// only ever gets the public half back, to paste into the target's own
// ~/.ssh/authorized_keys.
func generateHostKeyPair() (privatePEM string, authorizedKeyLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", msgs.Errorf("control.generatingKey", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "nkt-hub")
	if err != nil {
		return "", "", msgs.Errorf("hub.serializingPrivateKey", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", msgs.Errorf("hub.serializingPublicKey", err)
	}
	return string(pem.EncodeToMemory(block)), formatAuthorizedKey(sshPub), nil
}

// formatAuthorizedKey renders a public key as the single line
// ~/.ssh/authorized_keys expects.
func formatAuthorizedKey(pub ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
}
