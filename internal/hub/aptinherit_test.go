package hub

import (
	"context"
	"testing"

	"github.com/piqab/nkt/internal/store"
)

// Машина берёт apt через хаб от хоста, на котором стоит: кэш на хабе
// один, а проброс хаб держит до машины своим соединением, так что сеть
// машины наружу не открывается. Флаг хоста выключен — машина не
// трогается.
func TestInheritAptViaHub(t *testing.T) {
	m, db := newTestManager(t)
	ctx := context.Background()
	parentID, err := m.AddHost(ctx, "kvm", "10.0.0.1", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	machineID, err := m.AddHost(ctx, "vm", "192.168.123.50", 22, "root", store.HostAuthPassword, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetHostParent(ctx, machineID, parentID); err != nil {
		t.Fatal(err)
	}
	parent, err := db.HostByID(ctx, parentID)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.InheritAptViaHub(ctx, parent, machineID); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.HostByID(ctx, machineID); h.AptViaHub {
		t.Error("машина получила apt через хаб, которого у хоста нет")
	}

	if err := db.SetHostAptViaHub(ctx, parentID, true); err != nil {
		t.Fatal(err)
	}
	if parent, err = db.HostByID(ctx, parentID); err != nil {
		t.Fatal(err)
	}
	if err := m.InheritAptViaHub(ctx, parent, machineID); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.HostByID(ctx, machineID); !h.AptViaHub {
		t.Error("apt через хаб на машине не запомнился")
	}

	// Хост не может унаследовать от самого себя — и не должен терять свой
	// флаг: тот же вызов с его же идентификатором ничего не меняет.
	if err := m.InheritAptViaHub(ctx, parent, parentID); err != nil {
		t.Fatal(err)
	}
	if h, _ := db.HostByID(ctx, parentID); !h.AptViaHub {
		t.Error("хост потерял свой apt через хаб")
	}
}
