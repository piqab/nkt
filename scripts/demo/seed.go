//go:build ignore

// Демо-хаб для скриншотов сайта (scripts/screenshots.py): заводит в
// пустой базе хаба несколько хостов в группах, профиль и сценарий. Все
// хосты ведут на один и тот же локальный sshd с ключом, за которым nkt в
// режиме fixtures слушает 127.0.0.1:8077 — хаб видит их «в сети» и
// показывает настоящие данные, а не заглушки.
//
//	go run scripts/demo/seed.go -data /tmp/demo/hub -key /tmp/demo/ssh/client_key \
//	    -ssh-port 2299 -admin admin:пароль-fixtures
package main

import (
	"context"
	"flag"
	"log"
	"os"
	osuser "os/user"
	"path/filepath"
	"strings"

	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

func main() {
	dataDir := flag.String("data", "", "каталог данных хаба (NKT_DATA_DIR)")
	keyPath := flag.String("key", "", "приватный ключ SSH, которым хаб входит на хосты")
	sshPort := flag.Int("ssh-port", 2299, "порт локального sshd")
	admin := flag.String("admin", "admin:password", "учётная запись nkt на хосте (логин:пароль)")
	version := flag.String("version", "dev", "версия nkt, записанная хостам (как у хаба)")
	flag.Parse()
	if *dataDir == "" || *keyPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	ctx := context.Background()
	key, err := secretbox.ResolveKey("", filepath.Join(*dataDir, "hub.key"))
	must(err)
	db, err := store.Open(filepath.Join(*dataDir, "netknownsthat.db"))
	must(err)
	defer db.Close()

	pem, err := os.ReadFile(*keyPath)
	must(err)
	secretEnc, err := secretbox.Encrypt(key, pem)
	must(err)
	me, err := osuser.Current()
	must(err)
	adminUser, adminPass, _ := strings.Cut(*admin, ":")
	adminEnc, err := secretbox.Encrypt(key, []byte(adminPass))
	must(err)

	pid, err := db.CreateProfile(ctx, store.Profile{Name: "web-base", Color: "#5aa66f", Note: "базовый веб-хост", Content: `version: 1
name: web-base
packages:
  - nginx
  - certbot
  - htop
services:
  nginx:
    enabled: true
    active: true
firewall:
  - allow: 80/tcp
  - allow: 443/tcp
`})
	must(err)
	must(db.CreateHostGroupWithProfile(ctx, "production", pid))
	must(db.CreateHostGroup(ctx, "staging"))

	type host struct {
		name, group string
		profile     bool
	}
	for _, h := range []host{{"web-1", "production", true}, {"web-2", "production", true}, {"db-1", "production", false}, {"staging-1", "staging", false}} {
		id, err := db.CreateHost(ctx, h.name, "127.0.0.1", *sshPort, me.Username, store.HostAuthKey, secretEnc)
		must(err)
		must(db.SetHostAdmin(ctx, id, adminUser, adminEnc))
		must(db.SetHostStatus(ctx, id, store.HostStatusOnline, ""))
		must(db.SetHostGroup(ctx, id, h.group))
		must(db.SetHostArch(ctx, id, "amd64"))
		must(db.SetHostSudoStatus(ctx, id, store.SudoStatusNopasswd))
		must(db.SetHostVersion(ctx, id, *version))
		if h.profile {
			must(db.SetHostProfile(ctx, id, pid))
		}
	}
	_, err = db.CreateScript(ctx, store.Script{Name: "new-web-host", Color: "#5aa66f", Note: "пример из справки", Content: script.Examples[0].Content})
	must(err)
	log.Println("демо-хаб заполнен")
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
