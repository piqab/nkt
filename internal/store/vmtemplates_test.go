package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Одноимённый шаблон перезаписывается, а не плодит второй — и отвечает
// своим настоящим номером.
func TestSaveVMTemplateOverwritesByName(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	first, err := db.SaveVMTemplate(ctx, VMTemplate{Name: "малая", Spec: `{"vcpus":2}`})
	if err != nil {
		t.Fatalf("SaveVMTemplate: %v", err)
	}
	second, err := db.SaveVMTemplate(ctx, VMTemplate{Name: "малая", Spec: `{"vcpus":4}`})
	if err != nil {
		t.Fatalf("SaveVMTemplate (повтор): %v", err)
	}
	if second != first {
		t.Errorf("номер после перезаписи = %d, был %d", second, first)
	}

	list, err := db.ListVMTemplates(ctx)
	if err != nil {
		t.Fatalf("ListVMTemplates: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("шаблонов = %d, ожидался один", len(list))
	}
	if list[0].Spec != `{"vcpus":4}` {
		t.Errorf("описание не обновилось: %s", list[0].Spec)
	}
}

// Машина переезжает между группами вместе со своим хостом, и в базе это
// видно сразу: иначе разные части интерфейса показывали бы её то в одной
// группе, то в другой.
func TestHostGroupCarriesChildren(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "nkt.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	parent, err := db.CreateHost(ctx, "web1", "10.0.0.1", 22, "root", HostAuthPassword, []byte("x"))
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	child, err := db.CreateHost(ctx, "vm-1", "10.0.0.2", 22, "deploy", HostAuthPassword, []byte("x"))
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}
	if err := db.SetHostGroup(ctx, parent, "Прод"); err != nil {
		t.Fatalf("SetHostGroup: %v", err)
	}
	// Привязка сразу ставит машину в группу её хоста.
	if err := db.SetHostParent(ctx, child, parent); err != nil {
		t.Fatalf("SetHostParent: %v", err)
	}
	if h, _ := db.HostByID(ctx, child); h.Group != "Прод" {
		t.Errorf("после привязки группа машины = %q, want %q", h.Group, "Прод")
	}

	// Переезд хоста забирает машину с собой.
	if err := db.SetHostGroup(ctx, parent, "Резерв"); err != nil {
		t.Fatalf("SetHostGroup: %v", err)
	}
	if h, _ := db.HostByID(ctx, child); h.Group != "Резерв" {
		t.Errorf("машина осталась в %q, а хост переехал в «Резерв»", h.Group)
	}
}
