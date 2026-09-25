package vmcreate

import (
	"crypto/rand"
	"crypto/sha512"
	"strings"
)

// SHA-512-crypt ($6$, алгоритм Ульриха Дреппера) — тот хэш, который
// cloud-init кладёт в /etc/shadow из поля passwd: в user-data уходит он, а
// не сам пароль.

const cryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// HashPassword — $6$соль$хэш со случайной солью.
func HashPassword(password string) string {
	salt := make([]byte, 16)
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	for i := range salt {
		salt[i] = cryptAlphabet[int(buf[i])%len(cryptAlphabet)]
	}
	return sha512Crypt(password, string(salt))
}

func repeatTo(src []byte, n int) []byte {
	out := make([]byte, 0, n)
	for n > 0 {
		k := n
		if k > len(src) {
			k = len(src)
		}
		out = append(out, src[:k]...)
		n -= k
	}
	return out
}

func sha512Crypt(password, salt string) string {
	const rounds = 5000
	if len(salt) > 16 {
		salt = salt[:16]
	}
	p, s := []byte(password), []byte(salt)

	b := sha512.New()
	b.Write(p)
	b.Write(s)
	b.Write(p)
	B := b.Sum(nil)

	a := sha512.New()
	a.Write(p)
	a.Write(s)
	a.Write(repeatTo(B, len(p)))
	for i := len(p); i > 0; i >>= 1 {
		if i&1 != 0 {
			a.Write(B)
		} else {
			a.Write(p)
		}
	}
	A := a.Sum(nil)

	dp := sha512.New()
	for i := 0; i < len(p); i++ {
		dp.Write(p)
	}
	P := repeatTo(dp.Sum(nil), len(p))

	ds := sha512.New()
	for i := 0; i < 16+int(A[0]); i++ {
		ds.Write(s)
	}
	S := repeatTo(ds.Sum(nil), len(s))

	C := A
	for i := 0; i < rounds; i++ {
		c := sha512.New()
		if i&1 != 0 {
			c.Write(P)
		} else {
			c.Write(C)
		}
		if i%3 != 0 {
			c.Write(S)
		}
		if i%7 != 0 {
			c.Write(P)
		}
		if i&1 != 0 {
			c.Write(C)
		} else {
			c.Write(P)
		}
		C = c.Sum(nil)
	}

	var out strings.Builder
	out.WriteString("$6$" + salt + "$")
	enc := func(b2, b1, b0 byte, n int) {
		w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for ; n > 0; n-- {
			out.WriteByte(cryptAlphabet[w&0x3f])
			w >>= 6
		}
	}
	order := [][3]int{{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48},
		{28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13},
		{56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41}}
	for _, o := range order {
		enc(C[o[0]], C[o[1]], C[o[2]], 4)
	}
	enc(0, 0, C[63], 2)
	return out.String()
}
