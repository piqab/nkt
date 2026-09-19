package hub

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/piqab/nkt/internal/msgs"
	"strings"

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
	// Образы для кластеров — только список: файлы копируют руками, а
	// импорт скажет, каких не хватает.
	for _, img := range m.ClusterImages() {
		export.ClusterImages = append(export.ClusterImages, store.ClusterImageExport{Name: img.Name, Size: img.Size})
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
			return 0, []string{msgs.Tc(ctx, "hub.encryptionKeyFileCorrupted", err)}
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
		// Секреты кластеров — kubeconfig и план WireGuard — тем же ключом.
		okc := export.Clusters[:0]
		for _, c := range export.Clusters {
			reenc, err := reencryptClusterSecrets(oldKey, m.key, c)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", c.Name, err))
				continue
			}
			okc = append(okc, reenc)
		}
		export.Clusters = okc
		export.MasterKey = "" // never persisted; the point of this whole path is to not need it again
	}

	n, storeErrs := m.db.ImportHosts(ctx, export)
	errs = append(errs, storeErrs...)
	// Образы для кластеров: чего нет в библиотеке этого хаба.
	have := map[string]bool{}
	for _, img := range m.ClusterImages() {
		have[img.Name] = true
	}
	var missing []string
	for _, img := range export.ClusterImages {
		if !have[img.Name] {
			missing = append(missing, img.Name)
		}
	}
	if len(missing) > 0 {
		errs = append(errs, msgs.Tc(ctx, "hub.importImagesMissing", strings.Join(missing, ", "), m.clusterImagesDir()))
	}
	return n, errs
}

// reencryptClusterSecrets перешифровывает kubeconfig и план WireGuard
// кластера с ключа старого хаба на ключ этого.
func reencryptClusterSecrets(oldKey, newKey []byte, c store.ClusterExport) (store.ClusterExport, error) {
	for _, f := range []*[]byte{&c.KubeconfigEnc, &c.WGEnc} {
		if len(*f) == 0 {
			continue
		}
		raw, err := secretbox.Decrypt(oldKey, *f)
		if err != nil {
			return store.ClusterExport{}, msgs.Errorf("hub.decryptingClusterSecret", err)
		}
		enc, err := secretbox.Encrypt(newKey, raw)
		if err != nil {
			return store.ClusterExport{}, err
		}
		*f = enc
	}
	return c, nil
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
		return store.HostExport{}, msgs.Errorf("hub.decryptingSSHSecretBuiltKey", err)
	}
	secretEnc, err := secretbox.Encrypt(newKey, secret)
	if err != nil {
		return store.HostExport{}, msgs.Errorf("hub.reEncryptingSSHSecret", err)
	}
	h.SecretEnc = secretEnc

	if len(h.AdminPasswordEnc) > 0 {
		pw, err := secretbox.Decrypt(oldKey, h.AdminPasswordEnc)
		if err != nil {
			return store.HostExport{}, msgs.Errorf("hub.decryptingAdminPasswordBuiltKey", err)
		}
		pwEnc, err := secretbox.Encrypt(newKey, pw)
		if err != nil {
			return store.HostExport{}, msgs.Errorf("hub.reEncryptingAdminPassword", err)
		}
		h.AdminPasswordEnc = pwEnc
	}

	if len(h.TunnelTokenEnc) > 0 {
		token, err := secretbox.Decrypt(oldKey, h.TunnelTokenEnc)
		if err != nil {
			return store.HostExport{}, msgs.Errorf("hub.decryptingFallbackChannelTokenBuilt", err)
		}
		tokenEnc, err := secretbox.Encrypt(newKey, token)
		if err != nil {
			return store.HostExport{}, msgs.Errorf("hub.reEncryptingFallbackChannelToken", err)
		}
		h.TunnelTokenEnc = tokenEnc
	}
	return h, nil
}
