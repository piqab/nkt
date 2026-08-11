package hub

import "testing"

func TestMapUnameOS(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"Linux", "linux", false},
		{"linux", "linux", false},
		{"Darwin", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := mapUnameOS(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("mapUnameOS(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("mapUnameOS(%q): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("mapUnameOS(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMapUnameArch(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"x86_64", "amd64", false},
		{"amd64", "amd64", false},
		{"aarch64", "arm64", false},
		{"arm64", "arm64", false},
		{"armv7l", "", true},
		{"i386", "", true},
	}
	for _, c := range cases {
		got, err := mapUnameArch(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("mapUnameArch(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("mapUnameArch(%q): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("mapUnameArch(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
