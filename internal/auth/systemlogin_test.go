package auth

import "testing"

// Файлы взяты в том виде, в каком они есть на настоящей машине: у
// обычного пользователя своя одноимённая основная группа, а членство в
// sudo записано четвёртым полем строки группы.
const passwdSample = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
alex:x:1000:1000:alex,,,:/home/alex:/bin/bash
deploy:x:1001:1001::/home/deploy:/bin/bash
ops:x:1002:27::/home/ops:/bin/bash
`

const groupSample = `root:x:0:
sudo:x:27:alex,backup
wheel:x:998:
users:x:100:deploy
`

// Ворота, за которыми человек с учёткой на машине получает панель
// управления ею. Пускать всех подряд нельзя: обычный пользователь машины
// и её администратор — разные роли.
func TestUserIsAdmin(t *testing.T) {
	allowed := []string{
		"alex",   // перечислен в группе sudo
		"backup", // тоже в списке sudo, хотя в passwd его нет вовсе
		"ops",    // основная группа — 27, то есть sudo; в списке членов не значится
	}
	for _, user := range allowed {
		if !userIsAdmin(passwdSample, groupSample, user) {
			t.Errorf("%q не признан администратором, хотя состоит в sudo", user)
		}
	}

	denied := []string{
		"deploy", // обычный пользователь, только в users
		"daemon", // служебная учётка
		"",       // пустое имя не должно проходить ни при каких условиях
		"ale",    // частичное совпадение с alex
		"lex",
		"нетакого",
	}
	for _, user := range denied {
		if userIsAdmin(passwdSample, groupSample, user) {
			t.Errorf("%q признан администратором, хотя не состоит в sudo", user)
		}
	}
}

func TestPrimaryGroupName(t *testing.T) {
	if got := primaryGroupName(passwdSample, groupSample, "ops"); got != "sudo" {
		t.Errorf("основная группа ops = %q, ожидалась sudo", got)
	}
	if got := primaryGroupName(passwdSample, groupSample, "root"); got != "root" {
		t.Errorf("основная группа root = %q", got)
	}
	// Пользователя нет — пустая строка, а не паника и не «root».
	if got := primaryGroupName(passwdSample, groupSample, "нетакого"); got != "" {
		t.Errorf("несуществующий пользователь дал группу %q", got)
	}
}
