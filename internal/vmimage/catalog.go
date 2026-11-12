// Package vmimage качает и хранит облачные образы для создания машин.
//
// Сейчас раздел виртуализации умеет создать пустой диск и определить
// домен — дальше оператор ставит систему руками, как с физической
// машиной. Облачный образ закрывает этот разрыв: в нём система уже
// установлена, а cloud-init при первом запуске доводит её до пригодного
// к работе состояния.
package vmimage

import (
	"github.com/piqab/nkt/internal/msgs"
	"regexp"
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
	// FileName — имя, под которым образ ложится в кэш, и имя для списка.
	// У записей с FilePatterns — лишь ориентир: настоящее имя
	// разрешается из файла сумм при скачивании.
	FileName string `json:"file_name"`
	// FilePatterns — упорядоченные шаблоны имени образа в файле сумм
	// зеркала (порядок = предпочтение). Зеркала переименовывают образы
	// (Debian: genericcloud → generic), а файл сумм всегда перечисляет
	// то, что лежит в каталоге на самом деле, — разрешение имени по нему
	// переживает переименование без релиза nkt. Пусто — имя берётся
	// точно из FileName, как у своих образов.
	FilePatterns []string `json:"-"`
	// Checksum — сумма, заданная прямо здесь. Для каталога пусто (её
	// берут из ChecksumURL), для своего образа по ссылке — то, что ввёл
	// оператор. Пустая и то, и другое означает, что проверять нечем:
	// образ возьмётся как есть, о чём задание скажет вслух.
	Checksum string `json:"-"`
	// Custom — образ добавлен оператором, а не из каталога.
	Custom bool `json:"custom,omitempty"`
	// Direct — качать минуя кэш хаба, прямо с зеркала: копия в кэше
	// могла протухнуть или побиться, а оператору нужен заведомо свежий
	// файл.
	Direct bool `json:"-"`
}

// Catalog — образы, которые nkt умеет качать.
//
// Только Debian и Ubuntu: на них рассчитана вся остальная работа с
// хостами (apt, автонастройка), и предлагать образ, который потом
// нечем обслуживать, было бы нечестно.
var Catalog = []Image{
	{
		ID: "debian-13", Name: "Debian 13 (trixie)", OS: "debian", Arch: "amd64",
		URL:          "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.qcow2",
		ChecksumURL:  "https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS",
		ChecksumKind: SHA512,
		FileName:     "debian-13-generic-amd64.qcow2",
		// trixie переименовала genericcloud в generic; старое имя —
		// запасное, пока зеркало ещё отдаёт оба.
		FilePatterns: []string{`^debian-13-generic-amd64\.qcow2$`, `^debian-13-genericcloud-amd64\.qcow2$`},
	},
	{
		ID: "debian-12", Name: "Debian 12 (bookworm)", OS: "debian", Arch: "amd64",
		URL:          "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-generic-amd64.qcow2",
		ChecksumURL:  "https://cloud.debian.org/images/cloud/bookworm/latest/SHA512SUMS",
		ChecksumKind: SHA512,
		FileName:     "debian-12-generic-amd64.qcow2",
		FilePatterns: []string{`^debian-12-generic-amd64\.qcow2$`, `^debian-12-genericcloud-amd64\.qcow2$`},
	},
	{
		ID: "ubuntu-24.04", Name: "Ubuntu 24.04 LTS (noble)", OS: "ubuntu", Arch: "amd64",
		URL:          "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
		ChecksumURL:  "https://cloud-images.ubuntu.com/noble/current/SHA256SUMS",
		ChecksumKind: SHA256,
		FileName:     "noble-server-cloudimg-amd64.img",
		FilePatterns: []string{`^noble-server-cloudimg-amd64\.img$`, `^noble-server-cloudimg-amd64\.qcow2$`},
	},
	{
		ID: "ubuntu-22.04", Name: "Ubuntu 22.04 LTS (jammy)", OS: "ubuntu", Arch: "amd64",
		URL:          "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img",
		ChecksumURL:  "https://cloud-images.ubuntu.com/jammy/current/SHA256SUMS",
		ChecksumKind: SHA256,
		FileName:     "jammy-server-cloudimg-amd64.img",
		FilePatterns: []string{`^jammy-server-cloudimg-amd64\.img$`, `^jammy-server-cloudimg-amd64\.qcow2$`},
	},
}

// CustomPrefix — приставка идентификатора своего образа. Отличать его от
// каталожного надо: у каталожного есть ссылка и файл сумм, а у своего —
// только файл в кэше.
const CustomPrefix = "custom:"

// CustomImage строит запись по файлу, лежащему в кэше.
func CustomImage(fileName string) Image {
	return Image{
		ID:       CustomPrefix + fileName,
		Name:     fileName,
		FileName: fileName,
		Custom:   true,
	}
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

// fileNameRe — что годится в имя файла образа. Имя приходит от
// оператора и становится путём в каталоге кэша, поэтому ни косых черт,
// ни «..», ни пустоты.
var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// validFileName проверяет имя файла образа.
func validFileName(name string) bool {
	return fileNameRe.MatchString(name) && !strings.Contains(name, "..")
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
	return "", msgs.Errorf("vmimage.checksumFileHasLine", fileName)
}

// sumLines — строки файла сумм парами «имя, сумма» в порядке файла.
func sumLines(body string) [][2]string {
	var out [][2]string
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		out = append(out, [2]string{strings.TrimPrefix(fields[len(fields)-1], "*"), strings.ToLower(fields[0])})
	}
	return out
}

// resolveFromSums берёт из файла сумм имя образа и его сумму: по
// упорядоченным шаблонам записи (первый шаблон, под который попала
// строка, выигрывает), а без шаблонов — точным именем FileName. Файл
// сумм перечисляет то, что лежит в каталоге зеркала на самом деле,
// поэтому переименование образа на зеркале не ломает скачивание: имя
// приезжает оттуда, а не из кода.
func resolveFromSums(body string, img Image) (name, sum string, err error) {
	lines := sumLines(body)
	if len(img.FilePatterns) == 0 {
		for _, l := range lines {
			if l[0] == img.FileName {
				return l[0], l[1], nil
			}
		}
		return "", "", msgs.Errorf("vmimage.checksumFileHasLine", img.FileName)
	}
	for _, pat := range img.FilePatterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			return "", "", err
		}
		for _, l := range lines {
			if re.MatchString(l[0]) {
				return l[0], l[1], nil
			}
		}
	}
	return "", "", msgs.Errorf("vmimage.checksumFileNoMatch", img.Name)
}
