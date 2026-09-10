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
