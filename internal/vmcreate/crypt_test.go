package vmcreate

import (
	"strings"
	"testing"
)

// Эталоны — от openssl passwd -6 (и из описания алгоритма для первой).
func TestSHA512Crypt(t *testing.T) {
	cases := map[[2]string]string{
		{"Hello world!", "saltstring"}: "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1",
		{"пароль с пробелом и длинный-длинный-длинный-длинный-длинный-длинный-длинный-длинный", "abcdefgh"}: "$6$abcdefgh$XV17cgd9tNRg0gWVzmQAMFfBqxiqKq.MuOo55GyO8ieUddJ/KAbTRi4od1dbrl2KHdfFgoK2sIxL2/NBdZH5K/",
	}
	for in, want := range cases {
		if got := sha512Crypt(in[0], in[1]); got != want {
			t.Errorf("%q: %s, want %s", in[0], got, want)
		}
	}
	h := HashPassword("x")
	if !strings.HasPrefix(h, "$6$") || len(h) != 3+16+1+86 {
		t.Errorf("%s", h)
	}
}
