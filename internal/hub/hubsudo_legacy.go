package hub

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/piqab/nkt/internal/hubsudo"
	"github.com/piqab/nkt/internal/secretbox"
)

// Переезд на новый хаб при узком sudo. На хосте лежит открытый ключ того
// хаба, который его сузил; новый хаб подписывает своим ключом, и хост его
// бы отверг. Импорт полного экспорта (с мастер-ключом прежнего хаба)
// сохраняет здесь ключ подписи прежнего хаба. Хосту с прежним ключом хаб
// подписывает им же, а смену ключа на свой (OpRekey) делает, как только
// хост её умеет: доверие передаёт тот, кому хост уже доверял. Ключ
// прежнего хаба не даёт ничего сверх того, что он давал прежнему хабу.

// legacySignKV — ключи подписи прежних хабов (seed ed25519, base64),
// зашифрованные мастер-ключом этого хаба.
const legacySignKV = "hub.legacy_sign_keys"

// legacyKeys — кэш ключей прежних хабов (поле Manager).
type legacyKeys struct {
	mu   sync.Mutex
	keys []ed25519.PrivateKey
	ok   bool
}

// legacySignKeys — ключи прежних хабов (из kv, с кэшем).
func (m *Manager) legacySignKeys() []ed25519.PrivateKey {
	m.legacy.mu.Lock()
	defer m.legacy.mu.Unlock()
	if m.legacy.ok {
		return m.legacy.keys
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.legacy.keys, m.legacy.ok = nil, true
	raw, ok, err := m.db.KVGet(ctx, legacySignKV)
	if err != nil || !ok || raw == "" {
		return nil
	}
	plain, err := secretbox.Decrypt(m.key, []byte(raw))
	if err != nil {
		m.log.Warn("legacy hub signing keys not readable", "err", err)
		return nil
	}
	var seeds []string
	if json.Unmarshal(plain, &seeds) != nil {
		return nil
	}
	for _, s := range seeds {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == ed25519.SeedSize {
			m.legacy.keys = append(m.legacy.keys, ed25519.NewKeyFromSeed(b))
		}
	}
	return m.legacy.keys
}

// addLegacySignKeys — запомнить ключи прежних хабов (свой и дубли — нет).
func (m *Manager) addLegacySignKeys(ctx context.Context, keys ...ed25519.PrivateKey) error {
	own := m.signKey()
	have := m.legacySignKeys()
	list := append([]ed25519.PrivateKey{}, have...)
	added := false
	for _, k := range keys {
		if k == nil || bytes.Equal(k.Seed(), own.Seed()) {
			continue
		}
		dup := false
		for _, h := range list {
			if bytes.Equal(h.Seed(), k.Seed()) {
				dup = true
			}
		}
		if !dup {
			list, added = append(list, k), true
		}
	}
	if !added {
		return nil
	}
	seeds := make([]string, len(list))
	for i, k := range list {
		seeds[i] = base64.StdEncoding.EncodeToString(k.Seed())
	}
	plain, _ := json.Marshal(seeds)
	enc, err := secretbox.Encrypt(m.key, plain)
	if err != nil {
		return err
	}
	if err := m.db.KVSet(ctx, legacySignKV, string(enc)); err != nil {
		return err
	}
	m.legacy.mu.Lock()
	m.legacy.keys, m.legacy.ok = list, true
	m.legacy.mu.Unlock()
	return nil
}

// LegacySignSeeds — для полного экспорта: следующий хаб тоже сможет
// говорить с хостами, которые ещё не сменили ключ.
func (m *Manager) LegacySignSeeds() []string {
	var out []string
	for _, k := range m.legacySignKeys() {
		out = append(out, base64.StdEncoding.EncodeToString(k.Seed()))
	}
	return out
}

// importLegacySign — из полного экспорта: ключ прежнего хаба (из его
// мастер-ключа) и те, что он сам унаследовал.
func (m *Manager) importLegacySign(ctx context.Context, oldMaster []byte, seeds []string) error {
	keys := []ed25519.PrivateKey{hubsudo.KeyFromSecret(oldMaster)}
	for _, s := range seeds {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == ed25519.SeedSize {
			keys = append(keys, ed25519.NewKeyFromSeed(b))
		}
	}
	return m.addLegacySignKeys(ctx, keys...)
}

// keyFor — ключ, которому доверяет хост: свой или прежнего хаба.
// legacy — прежнего; ok — хоть какой-то наш.
func (m *Manager) keyFor(client *ssh.Client) (key ed25519.PrivateKey, legacy, ok bool) {
	out, err := runRemote(client, "cat "+hubsudo.PubKeyPath+" 2>/dev/null; true")
	if err != nil {
		return m.signKey(), false, false
	}
	return m.matchKey(out)
}

// matchKey — ключ по тексту открытого ключа на хосте.
func (m *Manager) matchKey(pubText string) (key ed25519.PrivateKey, legacy, ok bool) {
	have := strings.TrimSpace(pubText)
	own := m.signKey()
	if have == "" {
		return own, false, false
	}
	if have == strings.TrimSpace(hubsudo.PublicText(own)) {
		return own, false, true
	}
	for _, k := range m.legacySignKeys() {
		if have == strings.TrimSpace(hubsudo.PublicText(k)) {
			return k, true, true
		}
	}
	return own, false, false
}

// rekeyIfLegacy — хост доверяет прежнему хабу: сменить ключ на свой.
// Старый nkt операции не знает — не ошибка: хаб и дальше подписывает
// прежним ключом, смена — после обновления nkt.
func (m *Manager) rekeyIfLegacy(client *ssh.Client, sshUser string) bool {
	if sshUser == "root" {
		return false
	}
	if _, legacy, ok := m.keyFor(client); !ok || !legacy {
		return false
	}
	pub := strings.TrimSpace(hubsudo.PublicText(m.signKey()))
	out, err := m.hubSudo(client, sshUser, hubsudo.Request{Op: hubsudo.OpRekey, Args: map[string]string{"pub": pub}})
	if err != nil {
		m.log.Info("hub key on host not changed yet", "err", err, "out", strings.TrimSpace(out))
		return false
	}
	return true
}
