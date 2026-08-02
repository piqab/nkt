package model

import "testing"

func TestHostnameASCII(t *testing.T) {
	got, err := HostnameASCII("испытание.рф")
	if err != nil {
		t.Fatalf("HostnameASCII: %v", err)
	}
	if want := "xn--80akhbyknj4f.xn--p1ai"; got != want {
		t.Errorf("HostnameASCII(испытание.рф) = %q, want %q", got, want)
	}

	got, err = HostnameASCII("site.example.com")
	if err != nil || got != "site.example.com" {
		t.Errorf("HostnameASCII(site.example.com) = %q, %v, want unchanged", got, err)
	}
}

func TestHostnameUnicode(t *testing.T) {
	got := HostnameUnicode("xn--80akhbyknj4f.xn--p1ai")
	if want := "испытание.рф"; got != want {
		t.Errorf("HostnameUnicode = %q, want %q", got, want)
	}

	// Nothing to decode: must report "no footnote needed", not the input echoed back.
	if got := HostnameUnicode("site.example.com"); got != "" {
		t.Errorf("HostnameUnicode(plain ASCII) = %q, want empty", got)
	}
}
