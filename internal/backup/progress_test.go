package backup

import "testing"

// Проценты qemu-img и контрольные точки tar превращаются в шаг задания,
// служебные строки в журнал не идут.
func TestProgress(t *testing.T) {
	type ev struct {
		step int
		name string
	}
	var got []ev
	p := NewProgress(func(step int, name string) { got = append(got, ev{step, name}) })
	lines := []struct {
		in     string
		hidden bool
	}{
		{"--- copy vda: /var/lib/libvirt/images/web.qcow2", false},
		{"    (0.00/100%)", true},
		{"    (45.20/100%)", true},
		{"    (45.90/100%)", true}, // тот же процент — без повтора
		{"    (100.00/100%)", true},
		{"@@total 1024000 archive web.tar", true},
		{"tar: @@tarcp 50", true},
		{"tar: @@tarcp 200", true},
		{"@@phase virsh define", true},
		{"Domain web defined", false},
	}
	for _, l := range lines {
		if hidden := p.Line(l.in); hidden != l.hidden {
			t.Errorf("%q: hidden=%v", l.in, hidden)
		}
	}
	want := []ev{{0, "copy vda: /var/lib/libvirt/images/web.qcow2"}, {45, "copy vda: /var/lib/libvirt/images/web.qcow2"}, {100, "copy vda: /var/lib/libvirt/images/web.qcow2"},
		{0, "archive web.tar"}, {50, "archive web.tar"}, {99, "archive web.tar"}, {-1, "virsh define"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("#%d: %v, want %v", i, got[i], want[i])
		}
	}
}

// lxc export печатает проценты своей строкой — они тоже идут в шаг.
func TestProgressLXC(t *testing.T) {
	var last int
	var name string
	p := NewProgress(func(step int, n string) { last, name = step, n })
	p.Line("--- lxc export c1")
	if !p.Line("Exporting the backup: 45% (12.3MB/s)") || last != 45 || name != "lxc export c1" {
		t.Errorf("last=%d name=%q", last, name)
	}
}
