package hub

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// ExportHosts returns the hub's host registry for backup/migration.
// includeKey additionally embeds this hub's own master key (base64) in the
// export — a one-time bridge, not something meant to be kept around: it
// lets ImportHosts on a *different* hub decrypt each secret and
// immediately re-encrypt it with ITS OWN key (see ImportHosts), so after
// import nothing about the two hubs' keys needs to match ever again.
// Off by default (same reasoning as every other opt-in gate this project
// adds for something more sensitive than its neighbours): while the key is
// embedded, anyone holding the file can decrypt every secret in it — treat
// it exactly like a credentials file (not a shared drive, not email,
// delete once imported), same as without a key, just without also having
// to separately move NKT_HUB_MASTER_KEY.
func (m *Manager) ExportHosts(ctx context.Context, includeKey bool) (store.HubExport, error) {
	export, err := m.db.ExportHosts(ctx)
	if err != nil {
		return store.HubExport{}, err
	}
	if includeKey {
		export.MasterKey = base64.StdEncoding.EncodeToString(m.key)
	}
	return export, nil
}

// ImportHosts adds every host in export to this hub's registry via
// store.ImportHosts. When export carries a master key (see ExportHosts),
// each host's secrets are decrypted with THAT key and re-encrypted with
// this hub's own before being stored — a host whose secrets fail that step
// (a corrupted or mismatched embedded key) is dropped from the batch and
// reported in errs rather than stored with ciphertext this hub could never
// have decrypted anyway. Without an embedded key, ciphertext passes through
// unexamined exactly as store.ImportHosts does on its own — only usable if
// this hub's key already matches whatever produced the export.
func (m *Manager) ImportHosts(ctx context.Context, export store.HubExport) (imported int, errs []string) {
	if export.MasterKey != "" {
		oldKey, err := base64.StdEncoding.DecodeString(export.MasterKey)
		if err != nil {
			return 0, []string{fmt.Sprintf("ключ шифрования в файле повреждён: %v", err)}
		}

		ok := export.Hosts[:0]
		for _, h := range export.Hosts {
			reenc, err := reencryptHostSecrets(oldKey, m.key, h)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s (%s): %v", h.Name, h.Addr, err))
				continue
			}
			ok = append(ok, reenc)
		}
		export.Hosts = ok
		export.MasterKey = "" // never persisted; the point of this whole path is to not need it again
	}

	n, storeErrs := m.db.ImportHosts(ctx, export)
	return n, append(errs, storeErrs...)
}

// reencryptHostSecrets decrypts h's secret_enc/admin_password_enc/
// tunnel_token_enc with oldKey and re-encrypts them with newKey, leaving
// every other field untouched. Missing TunnelTokenEnc here (added along
// with the field itself, but originally overlooked in this specific
// re-encryption step) meant a host imported via "экспорт с ключом" kept a
// tunnel token still encrypted under the *old* hub's key — the new hub's
// own secretbox.Decrypt(m.key, ...) call in tunnelDialOnce would then
// simply fail every time, so the reverse-tunnel session this token gates
// could never come up, and every fallback dial silently had nothing to
// fall back to.
func reencryptHostSecrets(oldKey, newKey []byte, h store.HostExport) (store.HostExport, error) {
	secret, err := secretbox.Decrypt(oldKey, h.SecretEnc)
	if err != nil {
		return store.HostExport{}, fmt.Errorf("расшифровка SSH-секрета встроенным ключом: %w", err)
	}
	secretEnc, err := secretbox.Encrypt(newKey, secret)
	if err != nil {
		return store.HostExport{}, fmt.Errorf("перешифровка SSH-секрета: %w", err)
	}
	h.SecretEnc = secretEnc

	if len(h.AdminPasswordEnc) > 0 {
		pw, err := secretbox.Decrypt(oldKey, h.AdminPasswordEnc)
		if err != nil {
			return store.HostExport{}, fmt.Errorf("расшифровка admin-пароля встроенным ключом: %w", err)
		}
		pwEnc, err := secretbox.Encrypt(newKey, pw)
		if err != nil {
			return store.HostExport{}, fmt.Errorf("перешифровка admin-пароля: %w", err)
		}
		h.AdminPasswordEnc = pwEnc
	}

	if len(h.TunnelTokenEnc) > 0 {
		token, err := secretbox.Decrypt(oldKey, h.TunnelTokenEnc)
		if err != nil {
			return store.HostExport{}, fmt.Errorf("расшифровка токена резервного канала встроенным ключом: %w", err)
		}
		tokenEnc, err := secretbox.Encrypt(newKey, token)
		if err != nil {
			return store.HostExport{}, fmt.Errorf("перешифровка токена резервного канала: %w", err)
		}
		h.TunnelTokenEnc = tokenEnc
	}
	return h, nil
}
