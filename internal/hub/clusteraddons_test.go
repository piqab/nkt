package hub

import (
	"testing"

	"github.com/piqab/nkt/internal/k8s"
)

const testLonghornIndex = `apiVersion: v1
entries:
  longhorn:
  - version: 1.13.0
    kubeVersion: '>=1.34.0-0'
  - version: 1.13.0-rc1
  - version: 1.12.1
    kubeVersion: '>=1.25.0-0'
  - version: 1.12.0
  - version: 1.11.3
  - version: 1.10.2
  - version: 1.9.2
  - version: 1.8.3
  other:
  - version: 9.9.9
`

func TestParseAddonIndex(t *testing.T) {
	all, err := parseAddonIndex([]byte(testLonghornIndex), "longhorn")
	if err != nil || len(all) != 7 || all[0].Version != "1.13.0" || all[0].KubeVersion != ">=1.34.0-0" {
		t.Fatalf("%v %+v", err, all)
	}
	latest := addonLatestPatches(all, 4)
	var got []string
	for _, v := range latest {
		got = append(got, v.Version)
	}
	if len(got) != 4 || got[0] != "1.13.0" || got[1] != "1.12.1" || got[3] != "1.10.2" {
		t.Errorf("latest patches: %v", got)
	}
	if _, err := parseAddonIndex([]byte(testLonghornIndex), "rook"); err == nil {
		t.Error("no versions of an absent chart must be an error")
	}
}

func TestValidateAddons(t *testing.T) {
	list := []ClusterAddon{{Name: "longhorn"}}
	if err := validateAddons(list); err != nil || list[0].Version != k8s.LonghornDefaultVersion {
		t.Fatalf("%v %+v", err, list)
	}
	for _, bad := range [][]ClusterAddon{
		{{Name: "rook"}},
		{{Name: "longhorn", Version: "latest"}},
		{{Name: "longhorn"}, {Name: "longhorn"}},
	} {
		if validateAddons(bad) == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if addonReplicas(1) != 1 || addonReplicas(2) != 2 || addonReplicas(7) != 3 {
		t.Error("replicas")
	}
	spec := ClusterSpec{CPDiskGB: 30, WDiskGB: 15, Workers: 2}
	if spec.addonMinDisk() != 15 {
		t.Errorf("min disk %d", spec.addonMinDisk())
	}
	spec = ClusterSpec{Placements: []Placement{{Kind: KindVM, DiskGB: 40}, {Kind: KindHost}}}
	if spec.addonMinDisk() != 40 {
		t.Errorf("placements min disk %d", spec.addonMinDisk())
	}
}
