// Package vmimage качает и хранит облачные образы для создания машин.
//
// Сейчас раздел виртуализации умеет создать пустой диск и определить
// домен — дальше оператор ставит систему руками, как с физической
// машиной. Облачный образ закрывает этот разрыв: в нём система уже
// установлена, а cloud-init при первом запуске доводит её до пригодного
// к работе состояния.
package vmimage

import (
	"fmt"
	"strings"
)

// Checksum kinds.
const (
	SHA256 = "sha256"
	SHA512 = "sha512"
)

// Image — запись каталога.
type Image struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	OS   string `json:"os"`
	Arch string `json:"arch"`
	URL  string `json:"url"`
	// ChecksumURL — файл сумм в том же каталоге, что и образ. Сама сумма
	// не зашивается сюда: за ссылкой «latest» стоит регулярно
	// пересобираемый образ, и зашитая сумма протухла бы к следующей
	// сборке, превратив проверку в помеху.
	ChecksumURL  string `json:"-"`
	ChecksumKind string `json:"-"`
	// FileName — имя внутри файла сумм и имя, под которым образ ложится
	// в кэш.
	FileName string `json:"file_name"`
}

// Catalog — образы, которые nkt умеет качать.
//
// Только Debian и Ubuntu: на них рассчитана вся остальная работа с
// хостами (apt, автонастройка), и предлагать образ, который потом
// нечем обслуживать, было бы нечестно.
var Catalog = []Image{
	{
		ID: "debian-13", Name: "Debian 13 (trixie)", OS: "debian", Arch: "amd64",
		URL:          "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2",
		ChecksumURL:  "https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS",
		ChecksumKind: SHA512,
		FileName:     "debian-13-genericcloud-amd64.qcow2",
	},
	{
		ID: "debian-12", Name: "Debian 12 (bookworm)", OS: "debian", Arch: "amd64",
		URL:          "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
		ChecksumURL:  "https://cloud.debian.org/images/cloud/bookworm/latest/SHA512SUMS",
		ChecksumKind: SHA512,
		FileName:     "debian-12-genericcloud-amd64.qcow2",
	},
	{
		ID: "ubuntu-24.04", Name: "Ubuntu 24.04 LTS (noble)", OS: "ubuntu", Arch: "amd64",
		URL:          "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
		ChecksumURL:  "https://cloud-images.ubuntu.com/noble/current/SHA256SUMS",
		ChecksumKind: SHA256,
		FileName:     "noble-server-cloudimg-amd64.img",
	},
	{
		ID: "ubuntu-22.04", Name: "Ubuntu 22.04 LTS (jammy)", OS: "ubuntu", Arch: "amd64",
		URL:          "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img",
		ChecksumURL:  "https://cloud-images.ubuntu.com/jammy/current/SHA256SUMS",
		ChecksumKind: SHA256,
		FileName:     "jammy-server-cloudimg-amd64.img",
	},
}

// ByID находит образ каталога.
func ByID(id string) (Image, bool) {
	for _, img := range Catalog {
		if img.ID == id {
			return img, true
		}
	}
	return Image{}, false
}

// parseChecksums достаёт сумму нужного файла из файла сумм.
//
// Формат один у Debian и Ubuntu: «<сумма>  <имя файла>», иногда со
// звёздочкой перед именем (двоичный режим). Имя сравнивается целиком:
// в файле сумм рядом лежат и другие образы того же выпуска, и совпадение
// по подстроке выбрало бы не тот.
func parseChecksums(body, fileName string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == fileName {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("в файле сумм нет строки для %s", fileName)
}
