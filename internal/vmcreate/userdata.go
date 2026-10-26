package vmcreate

import (
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Свой user-data (1.17.4): владелец машины добавляет к настройкам
// первого запуска свой #cloud-config — пакеты, файлы, команды. Учётную
// запись, ключи и вход задаёт nkt: через них в машину входит хаб, поэтому
// эти ключи в своём user-data нельзя, а не «перезаписываются молча».

// UserDataMax — потолок своего user-data.
const UserDataMax = 64 << 10

// userDataReserved — ключи, которые задаёт nkt.
var userDataReserved = []string{
	"users", "user", "ssh_authorized_keys", "ssh_pwauth", "disable_root", "chpasswd", "password",
	"ssh_deletekeys", "ssh_keys", "hostname", "fqdn", "manage_etc_hosts", "growpart",
	"merge_how", "merge_type",
}

// userDataLists — списки, которые складываются со своими (у nkt там
// пакеты из Packages).
var userDataLists = []string{"packages", "runcmd", "bootcmd", "write_files"}

// CheckUserData проверяет свой user-data: только #cloud-config (без
// #include, скриптов и частей MIME), не больше 64 КБ, словарь YAML без
// ключей nkt.
func CheckUserData(text string) (map[string]any, error) {
	if len(text) > UserDataMax {
		return nil, msgs.Errorf("vmcreate.userDataTooLarge", UserDataMax>>10)
	}
	first, _, _ := strings.Cut(strings.TrimLeft(text, " \t\r\n"), "\n")
	if strings.TrimSpace(first) != "#cloud-config" {
		return nil, msgs.Errorf("vmcreate.userDataNotCloudConfig")
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, msgs.Errorf("vmcreate.userDataYAML", err)
	}
	for key := range doc {
		if slices.Contains(userDataReserved, key) {
			return nil, msgs.Errorf("vmcreate.userDataReserved", key)
		}
	}
	return doc, nil
}

// mergeUserData — настройки nkt и свой user-data одним #cloud-config.
func mergeUserData(base, own string) (string, error) {
	extra, err := CheckUserData(own)
	if err != nil {
		return "", err
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(base), &doc); err != nil {
		return "", err
	}
	for key, v := range extra {
		if cur, ok := doc[key].([]any); ok && slices.Contains(userDataLists, key) {
			if more, ok := v.([]any); ok {
				doc[key] = append(cur, more...)
				continue
			}
		}
		doc[key] = v
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", err
	}
	return "#cloud-config\n" + string(out), nil
}
