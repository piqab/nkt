package vmcreate

import "golang.org/x/crypto/bcrypt"

// HashPassword — bcrypt-хэш ($2a$) для поля passwd в cloud-init: в
// /etc/shadow ложится он, а не пароль. libxcrypt (Debian, Ubuntu, Fedora)
// и musl (Alpine) такие хэши понимают. Стоимость 12 — медленный подбор,
// но создание машины это не задерживает заметно.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(h), nil
}
