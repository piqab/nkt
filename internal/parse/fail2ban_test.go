package parse

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

// TestFail2banDiscoversLocalOverridesOnly confirms jail.local and
// jail.d/*.conf are listed as editable, but the package-shipped jail.conf
// never is — matching fail2ban's own documented guidance that jail.conf
// gets overwritten on upgrade and local overrides belong in jail.local/
// jail.d instead.
func TestFail2banDiscoversLocalOverridesOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "jail.conf"), []byte("[DEFAULT]\n"), 0o644); err != nil {
		t.Fatalf("write jail.conf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "jail.local"), []byte("[sshd]\nenabled = true\n"), 0o644); err != nil {
		t.Fatalf("write jail.local: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "jail.d"), 0o755); err != nil {
		t.Fatalf("mkdir jail.d: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "jail.d", "custom.conf"), []byte("[nginx-http-auth]\n"), 0o644); err != nil {
		t.Fatalf("write jail.d/custom.conf: %v", err)
	}

	c := collect.NewLocal("", "", 5*time.Second)
	files := Fail2ban(c, root)

	byPath := map[string]model.ManagedFile{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	jailLocalPath := filepath.Join(root, "jail.local")
	jailDPath := filepath.Join(root, "jail.d", "custom.conf")
	jailConfPath := filepath.Join(root, "jail.conf")

	if _, ok := byPath[jailLocalPath]; !ok {
		t.Errorf("jail.local not discovered; got %+v", files)
	} else if !byPath[jailLocalPath].Editable || !byPath[jailLocalPath].Readable {
		t.Errorf("jail.local = %+v, want Editable and Readable both true", byPath[jailLocalPath])
	}
	if _, ok := byPath[jailDPath]; !ok {
		t.Errorf("jail.d/custom.conf not discovered; got %+v", files)
	}
	if _, ok := byPath[jailConfPath]; ok {
		t.Error("jail.conf (the package-shipped default) was listed — it must never be, see Fail2ban's own comment")
	}
	for _, f := range files {
		if f.Service != model.ServiceFail2ban {
			t.Errorf("file %q has Service = %q, want %q", f.Path, f.Service, model.ServiceFail2ban)
		}
	}
}

// TestFail2banNoLocalOverrideYet confirms a host with only the
// package-shipped jail.conf (no jail.local/jail.d created) reports no
// editable files at all, rather than erroring.
func TestFail2banNoLocalOverrideYet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "jail.conf"), []byte("[DEFAULT]\n"), 0o644); err != nil {
		t.Fatalf("write jail.conf: %v", err)
	}

	c := collect.NewLocal("", "", 5*time.Second)
	files := Fail2ban(c, root)
	if len(files) != 0 {
		t.Errorf("Fail2ban() = %+v, want no files with neither jail.local nor jail.d present", files)
	}
}
