package osinfo

import "testing"

func TestFromOSRelease(t *testing.T) {
	text := "PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nNAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\n"
	o := FromOSRelease(text, SourceOSRelease)
	if o == nil || o.ID != "ubuntu" || o.Name != "Ubuntu 24.04.1 LTS" || o.Like != "debian" {
		t.Fatalf("%+v", o)
	}
	if o := FromOSRelease("NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.20.3\n", SourceOSRelease); o == nil || o.ID != "alpine" || o.Name != "Alpine Linux" {
		t.Fatalf("%+v", o)
	}
	if o := FromOSRelease("ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\nPRETTY_NAME=\"Rocky Linux 9.4\"\n", SourceOSRelease); o.ID != "rocky" || o.Like != "rhel" {
		t.Fatalf("%+v", o)
	}
	if FromOSRelease("", SourceOSRelease) != nil {
		t.Fatal("пустой os-release")
	}
}

func TestFromAgent(t *testing.T) {
	o := FromAgent([]byte(`{"return":{"name":"Microsoft Windows","kernel-release":"19045","version":"Microsoft Windows 10","pretty-name":"Windows 10 Pro","version-id":"10","id":"mswindows","machine":"x86_64"}}`))
	if o == nil || o.ID != "windows" || o.Name != "Windows 10 Pro" || o.Source != SourceAgent {
		t.Fatalf("%+v", o)
	}
	if o := FromAgent([]byte(`{"return":{"id":"debian","name":"Debian GNU/Linux","pretty-name":"Debian GNU/Linux 12 (bookworm)"}}`)); o.ID != "debian" {
		t.Fatalf("%+v", o)
	}
	if FromAgent([]byte(`{"error":{"class":"CommandNotFound"}}`)) != nil {
		t.Fatal("ошибка агента")
	}
}

func TestFromLibosinfo(t *testing.T) {
	cases := map[string][2]string{
		"http://microsoft.com/win/11":             {"windows", "Windows 11"},
		"http://microsoft.com/win/2k22":           {"windows", "Windows 2k22"},
		"http://ubuntu.com/ubuntu/24.04":          {"ubuntu", "Ubuntu 24.04"},
		"http://debian.org/debian/12":             {"debian", "Debian 12"},
		"http://alpinelinux.org/alpinelinux/3.19": {"alpine", "Alpine Linux 3.19"},
		"http://redhat.com/rhel/9.3":              {"rhel", "Red Hat Enterprise Linux 9.3"},
		"http://libosinfo.org/linux/2022":         {"linux", "linux 2022"},
	}
	for id, want := range cases {
		o := FromLibosinfo(id)
		if o == nil || o.ID != want[0] || o.Name != want[1] || o.Source != SourceLibosinfo {
			t.Errorf("%s → %+v", id, o)
		}
	}
	if FromLibosinfo("garbage") != nil {
		t.Fatal("не URL")
	}
}

func TestGuessAndImage(t *testing.T) {
	if o := GuessWindows("<domain><features><acpi/><hyperv mode='custom'><relaxed state='on'/></hyperv></features></domain>"); o == nil || o.ID != "windows" || o.Source != SourceGuess {
		t.Fatalf("%+v", o)
	}
	if GuessWindows("<domain><features><acpi/></features></domain>") != nil {
		t.Fatal("без hyperv")
	}
	if o := FromImageKeys("Ubuntu", "noble", "ubuntu 24.04 LTS amd64 (release) (20240821)"); o.ID != "ubuntu" || o.Source != SourceImage {
		t.Fatalf("%+v", o)
	}
	if o := FromImageKeys("Alpinelinux", "3.20", ""); o.ID != "alpine" || o.Name != "Alpinelinux 3.20" {
		t.Fatalf("%+v", o)
	}
	if FromImageKeys("", "", "") != nil {
		t.Fatal("пусто")
	}
}
