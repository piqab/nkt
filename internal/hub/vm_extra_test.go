package hub

import (
	"testing"

	"github.com/piqab/nkt/internal/model"
)

// Строка машины без своего nkt (Windows) берёт ОС и экраны из списка
// доменов её хоста по последнему опросу.
func TestVMExtra(t *testing.T) {
	m := &Manager{overview: map[int64]hostOverview{}, hostOS: map[int64]*model.OSInfo{}}
	m.overview[7] = hostOverview{
		vmStates: map[string]string{"win11": "running"},
		vmExtra:  map[string]vmExtra{"win11": {os: &model.OSInfo{ID: "windows", Name: "Windows 11 Pro", Source: "agent"}, graphics: []string{"spice"}}},
	}
	os, g := m.VMExtra(7, "win11")
	if os == nil || os.ID != "windows" || len(g) != 1 || g[0] != "spice" {
		t.Fatalf("%+v %v", os, g)
	}
	if os, g := m.VMExtra(7, "nope"); os != nil || g != nil {
		t.Fatal("чужая машина")
	}
	if os, _ := m.VMExtra(99, "win11"); os != nil {
		t.Fatal("неопрошенный хост")
	}
}
