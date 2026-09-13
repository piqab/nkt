// Package script — сценарии хаба: короткий построчный язык, которым
// описывают, что развернуть на хостах, и который хаб выполняет теми же
// вызовами, что и кнопки интерфейса.
//
// Язык нарочно узкий: одна строка — одно действие, слова — те же, что в
// интерфейсе, никаких выражений и ветвлений. Сценарий читают как список
// дел, а не как программу; всё, что сложнее, делают профили и compose.
//
//	# Веб-ферма
//	group web-farm profile web-base
//	host web1 192.0.2.10 user root password ask
//	install web1
//	on web1 packages install nginx htop
//	on web1 docker stack /srv/app/docker-compose.yml up
//	services:
//	  app:
//	    image: nginx
//	end
//
// Команды, их аргументы и справка описаны одной таблицей (Commands):
// из неё же строится и разбор, и раздел «Справка» — разойтись они не
// могут.
package script

// Kind — вид шага.
type Kind string

const (
	KindSet          Kind = "set"
	KindGroup        Kind = "group"
	KindHost         Kind = "host"
	KindInstall      Kind = "install"
	KindWait         Kind = "wait"
	KindPackages     Kind = "packages"
	KindService      Kind = "service"
	KindFirewall     Kind = "firewall"
	KindDockerInst   Kind = "docker.install"
	KindDockerStack  Kind = "docker.stack"
	KindVMCreate     Kind = "vm.create"
	KindVMAction     Kind = "vm.action"
	KindApplyProfile Kind = "apply"
	KindFilePut      Kind = "file.put"
)

// ArgDoc — один аргумент команды в справке.
type ArgDoc struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	// Desc — ключ msgs.
	Desc string `json:"desc"`
}

// CommandDoc — команда в справке. Тексты — ключи каталога msgs, чтобы
// справка была на языке интерфейса.
type CommandDoc struct {
	Kind Kind `json:"kind"`
	// Syntax — как пишется, с плейсхолдерами.
	Syntax string `json:"syntax"`
	// Summary — ключ msgs с описанием.
	Summary string   `json:"summary"`
	Args    []ArgDoc `json:"args,omitempty"`
	Example string   `json:"example"`
	// Block — команда принимает блок строк до «end».
	Block bool `json:"block,omitempty"`
	// OnHost — команда пишется как «on <хост> …».
	OnHost bool `json:"on_host,omitempty"`
}

// Commands — вся таблица языка в порядке, в котором её показывает
// справка: от «завести» к «сделать на хосте».
var Commands = []CommandDoc{
	{
		Kind: KindSet, Syntax: "set ИМЯ значение",
		Summary: "script.doc.set",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.set.name"},
			{Name: "значение", Required: true, Desc: "script.doc.set.value"},
		},
		Example: "set IMAGE ubuntu-24.04\non web1 vm create app1 image ${IMAGE}",
	},
	{
		Kind: KindGroup, Syntax: "group ИМЯ [profile ПРОФИЛЬ]",
		Summary: "script.doc.group",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.group.name"},
			{Name: "profile", Desc: "script.doc.group.profile"},
		},
		Example: "group web-farm profile web-base",
	},
	{
		Kind: KindHost, Syntax: "host ИМЯ АДРЕС[:ПОРТ] user ПОЛЬЗОВАТЕЛЬ (password ask | password \"…\" | key hub) [group ГРУППА]",
		Summary: "script.doc.host",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.host.name"},
			{Name: "АДРЕС", Required: true, Desc: "script.doc.host.addr"},
			{Name: "user", Required: true, Desc: "script.doc.host.user"},
			{Name: "password ask", Desc: "script.doc.host.passwordAsk"},
			{Name: "password \"…\"", Desc: "script.doc.host.password"},
			{Name: "key hub", Desc: "script.doc.host.keyHub"},
			{Name: "group", Desc: "script.doc.host.group"},
		},
		Example: "host web1 192.0.2.10 user root password ask group web-farm\nhost web2 192.0.2.11:2222 user deploy key hub",
	},
	{
		Kind: KindInstall, Syntax: "install ХОСТ…",
		Summary: "script.doc.install",
		Args:    []ArgDoc{{Name: "ХОСТ", Required: true, Desc: "script.doc.install.host"}},
		Example: "install web1 web2",
	},
	{
		Kind: KindWait, Syntax: "wait ХОСТ online [ДЛИТЕЛЬНОСТЬ]",
		Summary: "script.doc.wait",
		Args: []ArgDoc{
			{Name: "ХОСТ", Required: true, Desc: "script.doc.wait.host"},
			{Name: "ДЛИТЕЛЬНОСТЬ", Desc: "script.doc.wait.duration"},
		},
		Example: "wait web1 online 5m",
	},
	{
		Kind: KindPackages, OnHost: true, Syntax: "on ХОСТ packages install|remove ПАКЕТ…",
		Summary: "script.doc.packages",
		Args: []ArgDoc{
			{Name: "install|remove", Required: true, Desc: "script.doc.packages.action"},
			{Name: "ПАКЕТ", Required: true, Desc: "script.doc.packages.pkg"},
		},
		Example: "on web1 packages install nginx htop",
	},
	{
		Kind: KindService, OnHost: true, Syntax: "on ХОСТ service ИМЯ start|stop|restart|reload|enable|disable",
		Summary: "script.doc.service",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.service.name"},
			{Name: "действие", Required: true, Desc: "script.doc.service.action"},
		},
		Example: "on web1 service nginx restart",
	},
	{
		Kind: KindFirewall, OnHost: true, Syntax: "on ХОСТ firewall allow|deny ПОРТ[/tcp|udp] [from CIDR]",
		Summary: "script.doc.firewall",
		Args: []ArgDoc{
			{Name: "allow|deny", Required: true, Desc: "script.doc.firewall.action"},
			{Name: "ПОРТ", Required: true, Desc: "script.doc.firewall.port"},
			{Name: "from", Desc: "script.doc.firewall.from"},
		},
		Example: "on web1 firewall allow 443/tcp\non web1 firewall allow 5432 from 10.0.0.0/24",
	},
	{
		Kind: KindDockerInst, OnHost: true, Syntax: "on ХОСТ docker install",
		Summary: "script.doc.dockerInstall",
		Example: "on web1 docker install",
	},
	{
		Kind: KindDockerStack, OnHost: true, Block: true, Syntax: "on ХОСТ docker stack ПУТЬ up|down\n  [compose-файл построчно]\nend",
		Summary: "script.doc.dockerStack",
		Args: []ArgDoc{
			{Name: "ПУТЬ", Required: true, Desc: "script.doc.dockerStack.path"},
			{Name: "up|down", Required: true, Desc: "script.doc.dockerStack.action"},
		},
		Example: "on web1 docker stack /srv/app/docker-compose.yml up\nservices:\n  web:\n    image: nginx:alpine\n    ports:\n      - \"8080:80\"\nend",
	},
	{
		Kind: KindVMCreate, OnHost: true, Syntax: "on ХОСТ vm create ИМЯ image ОБРАЗ [cpu N] [mem МБ] [disk ГБ] [user ИМЯ] [network СЕТЬ] [profile ПРОФИЛЬ] [install]",
		Summary: "script.doc.vmCreate",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.vmCreate.name"},
			{Name: "image", Required: true, Desc: "script.doc.vmCreate.image"},
			{Name: "cpu / mem / disk", Desc: "script.doc.vmCreate.size"},
			{Name: "user", Desc: "script.doc.vmCreate.user"},
			{Name: "network", Desc: "script.doc.vmCreate.network"},
			{Name: "profile", Desc: "script.doc.vmCreate.profile"},
			{Name: "install", Desc: "script.doc.vmCreate.install"},
		},
		Example: "on web1 vm create app1 image ubuntu-24.04 cpu 2 mem 2048 disk 20 profile app install",
	},
	{
		Kind: KindVMAction, OnHost: true, Syntax: "on ХОСТ vm start|shutdown|destroy ИМЯ",
		Summary: "script.doc.vmAction",
		Args: []ArgDoc{
			{Name: "действие", Required: true, Desc: "script.doc.vmAction.action"},
			{Name: "ИМЯ", Required: true, Desc: "script.doc.vmAction.name"},
		},
		Example: "on web1 vm start app1",
	},
	{
		Kind: KindApplyProfile, OnHost: true, Syntax: "on ХОСТ apply profile ПРОФИЛЬ",
		Summary: "script.doc.apply",
		Args:    []ArgDoc{{Name: "ПРОФИЛЬ", Required: true, Desc: "script.doc.apply.profile"}},
		Example: "on web2 apply profile web-base",
	},
	{
		Kind: KindFilePut, OnHost: true, Block: true, Syntax: "on ХОСТ file put ПУТЬ [mode 0644]\n  [содержимое построчно]\nend",
		Summary: "script.doc.filePut",
		Args: []ArgDoc{
			{Name: "ПУТЬ", Required: true, Desc: "script.doc.filePut.path"},
			{Name: "mode", Desc: "script.doc.filePut.mode"},
		},
		Example: "on web1 file put /etc/motd\nЭтот сервер под управлением nkt\nend",
	},
}

// docByKind — команда справки по виду шага.
func docByKind(k Kind) *CommandDoc {
	for i := range Commands {
		if Commands[i].Kind == k {
			return &Commands[i]
		}
	}
	return nil
}
