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

import "strings"

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
	KindParam        Kind = "param"
	KindUserAdd      Kind = "user.add"
	KindSystem       Kind = "system"
	KindCert         Kind = "cert"
	KindGitClone     Kind = "git.clone"
	KindK8sCreate    Kind = "k8s.create"
	KindK8sDestroy   Kind = "k8s.destroy"
	KindPipeline     Kind = "pipeline"
	KindDeploy       Kind = "deploy"
	KindMigrate      Kind = "migrate"
	KindK8sIngress   Kind = "k8s.ingress"
	KindK8sApply     Kind = "k8s.apply"
	KindK8sWait      Kind = "k8s.wait"
	KindFlyRoute     Kind = "fly.route"
	KindFlyApply     Kind = "fly.apply"
	KindFlyOwn       Kind = "fly.own"
	KindPlugin       Kind = "plugin"
	// KindRun — команда на хосте плагина (MikroTik, Windows…) на языке
	// его типа: «on ХОСТ run "КОМАНДА"» или блок до «end».
	KindRun Kind = "run"
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
		Kind: KindSet, Syntax: "set ИМЯ значение | set ИМЯ ask",
		Summary: "script.doc.set",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.set.name"},
			{Name: "значение", Required: true, Desc: "script.doc.set.value"},
			{Name: "ask", Desc: "script.doc.set.ask"},
		},
		Example: "set IMAGE ubuntu-24.04\nset DB_PASS ask\non web1 vm create app1 image ${IMAGE}",
	},
	{
		Kind: KindParam, Syntax: "param ИМЯ \"подсказка\"",
		Summary: "script.doc.param",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.param.name"},
			{Name: "подсказка", Desc: "script.doc.param.prompt"},
		},
		Example: "param ADDR \"адрес нового сервера\"\nparam NAME \"имя хоста в хабе\"\nhost ${NAME} ${ADDR} user root password ask",
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
		Kind: KindWait, Syntax: "wait ХОСТ online [ДЛИТЕЛЬНОСТЬ]\nwait ХОСТ port ПОРТ [ДЛИТЕЛЬНОСТЬ]\nwait ХОСТ http URL [КОД] [ДЛИТЕЛЬНОСТЬ]\nwait ХОСТ service ИМЯ active [ДЛИТЕЛЬНОСТЬ]\nwait url URL [КОД] [ДЛИТЕЛЬНОСТЬ]",
		Summary: "script.doc.wait",
		Args: []ArgDoc{
			{Name: "ХОСТ", Required: true, Desc: "script.doc.wait.host"},
			{Name: "online", Desc: "script.doc.wait.online"},
			{Name: "port", Desc: "script.doc.wait.port"},
			{Name: "http", Desc: "script.doc.wait.http"},
			{Name: "service", Desc: "script.doc.wait.service"},
			{Name: "url", Desc: "script.doc.wait.url"},
			{Name: "ДЛИТЕЛЬНОСТЬ", Desc: "script.doc.wait.duration"},
		},
		Example: "wait web1 online 5m\nwait web1 port 443 30s\nwait web1 http https://example.org/ 200 1m\nwait web1 service nginx active\nwait url https://git.example.com/api/healthz 200 10m",
	},
	{
		Kind: KindPackages, OnHost: true, Syntax: "on ХОСТ… packages install|remove ПАКЕТ…",
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
		Kind: KindDockerStack, OnHost: true, Block: true, Syntax: "on ХОСТ docker stack ПУТЬ up|down|start|stop\n[compose-файл построчно\nend]",
		Summary: "script.doc.dockerStack",
		Args: []ArgDoc{
			{Name: "ПУТЬ", Required: true, Desc: "script.doc.dockerStack.path"},
			{Name: "up|down|start|stop", Required: true, Desc: "script.doc.dockerStack.action"},
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
		Kind: KindVMAction, OnHost: true, Syntax: "on ХОСТ vm start|shutdown|destroy|reboot|delete ИМЯ · vm snapshot|revert ИМЯ СНИМОК · vm resize ИМЯ [cpu N] [mem МБ] [disk ГБ]",
		Summary: "script.doc.vmAction",
		Args: []ArgDoc{
			{Name: "действие", Required: true, Desc: "script.doc.vmAction.action"},
			{Name: "ИМЯ", Required: true, Desc: "script.doc.vmAction.name"},
			{Name: "СНИМОК", Desc: "script.doc.vmAction.snap"},
			{Name: "cpu / mem / disk", Desc: "script.doc.vmAction.resize"},
		},
		Example: "on web1 vm snapshot app1 before-upgrade",
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
	{
		Kind: KindUserAdd, OnHost: true, Syntax: "on ХОСТ user add ИМЯ [sudo] [key \"ssh-ed25519 …\"]",
		Summary: "script.doc.userAdd",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.userAdd.name"},
			{Name: "sudo", Desc: "script.doc.userAdd.sudo"},
			{Name: "key", Desc: "script.doc.userAdd.key"},
		},
		Example: "on web1 user add deploy sudo key \"ssh-ed25519 AAAAC3Nza… deploy@laptop\"",
	},
	{
		Kind: KindSystem, OnHost: true, Syntax: "on ХОСТ system hostname ИМЯ | timezone ЗОНА | locale ЛОКАЛЬ | ntp СЕРВЕР…",
		Summary: "script.doc.system",
		Args: []ArgDoc{
			{Name: "hostname", Desc: "script.doc.system.hostname"},
			{Name: "timezone", Desc: "script.doc.system.timezone"},
			{Name: "locale", Desc: "script.doc.system.locale"},
			{Name: "ntp", Desc: "script.doc.system.ntp"},
		},
		Example: "on web1 system hostname web1\non web1 system timezone Europe/Moscow\non web1 system locale ru_RU.UTF-8\non web1 system ntp time.google.com pool.ntp.org",
	},
	{
		Kind: KindCert, OnHost: true, Syntax: "on ХОСТ cert issue ДОМЕН… | cert renew ДОМЕН",
		Summary: "script.doc.cert",
		Args: []ArgDoc{
			{Name: "issue", Desc: "script.doc.cert.issue"},
			{Name: "renew", Desc: "script.doc.cert.renew"},
		},
		Example: "on web1 cert issue example.org www.example.org\non web1 cert renew example.org",
	},
	{
		Kind: KindGitClone, OnHost: true, Syntax: "on ХОСТ git clone URL /КАТАЛОГ [branch ВЕТКА] [token ask | key hub]",
		Summary: "script.doc.gitClone",
		Args: []ArgDoc{
			{Name: "URL", Required: true, Desc: "script.doc.gitClone.url"},
			{Name: "/КАТАЛОГ", Required: true, Desc: "script.doc.gitClone.dir"},
			{Name: "branch", Desc: "script.doc.gitClone.branch"},
			{Name: "token ask", Desc: "script.doc.gitClone.token"},
			{Name: "key hub", Desc: "script.doc.gitClone.key"},
		},
		Example: "on web1 git clone https://github.com/org/app.git /srv/app branch main token ask",
	},
	{
		Kind: KindK8sCreate, OnHost: true, Syntax: "on ХОСТ k8s create ИМЯ [flavor k3s|kubeadm] [nodes single|1+N|3+N|\"ХОСТ: cp N, w N, host cp|w, bridge МОСТ, endpoint АДРЕС; …\"] [image ОБРАЗ] [cpu N] [mem МБ] [disk ГБ] [network СЕТЬ|nat|bridge|wireguard] [bridge МОСТ] [cni cilium|flannel] [no-kube-proxy] [version 1.34] [addon longhorn[=ВЕРСИЯ]] [expose] [api ПОРТ] [http ПОРТ] [https ПОРТ]",
		Summary: "script.doc.k8sCreate",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.k8sCreate.name"},
			{Name: "flavor", Desc: "script.doc.k8sCreate.flavor"},
			{Name: "nodes", Desc: "script.doc.k8sCreate.nodes"},
			{Name: "image", Desc: "script.doc.k8sCreate.image"},
			{Name: "cpu / mem / disk", Desc: "script.doc.k8sCreate.sizes"},
			{Name: "network / bridge", Desc: "script.doc.k8sCreate.network"},
			{Name: "cni", Desc: "script.doc.k8sCreate.cni"},
			{Name: "no-kube-proxy", Desc: "script.doc.k8sCreate.noKubeProxy"},
			{Name: "version", Desc: "script.doc.k8sCreate.version"},
			{Name: "addon", Desc: "script.doc.k8sCreate.addon"},
			{Name: "expose", Desc: "script.doc.k8sCreate.expose"},
			{Name: "api / http / https", Desc: "script.doc.k8sCreate.ports"},
		},
		Example: "on hv1 k8s create lab nodes \"hv1: cp 1, w 2; hv2: w 2; hv3: host w\" network wireguard cni cilium expose api 16443",
	},
	{
		Kind: KindK8sDestroy, OnHost: true, Syntax: "on ХОСТ k8s destroy ИМЯ",
		Summary: "script.doc.k8sDestroy",
		Args:    []ArgDoc{{Name: "ИМЯ", Required: true, Desc: "script.doc.k8sDestroy.name"}},
		Example: "on hv1 k8s destroy lab",
	},
	{
		Kind: KindPipeline, Syntax: "pipeline ИМЯ\n  описание конвейера построчно\nend",
		Summary: "script.doc.pipeline", Block: true,
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.pipeline.name"},
		},
		Example: "pipeline hello\nrepo: https://github.com/org/app.git\nref: main\naction: compose\ncompose:\n  file: deploy/docker-compose.yml\n  project: hello\n  hosts: [web1]\n  env:\n    DB_PASSWORD: generate:password\nend",
	},
	{
		Kind: KindDeploy, Syntax: "deploy КОНВЕЙЕР [start|stop] [ДЛИТЕЛЬНОСТЬ]",
		Summary: "script.doc.deploy",
		Args: []ArgDoc{
			{Name: "КОНВЕЙЕР", Required: true, Desc: "script.doc.deploy.name"},
			{Name: "start|stop", Desc: "script.doc.deploy.power"},
			{Name: "ДЛИТЕЛЬНОСТЬ", Desc: "script.doc.deploy.duration"},
		},
		Example: "deploy hello 20m\ndeploy shop stop\ndeploy shop start",
	},
	{
		Kind: KindMigrate, Syntax: "migrate ХОСТ КОНТЕЙНЕР to ХОСТ [switch-fly] [switch-pipeline] [clean-leftovers]",
		Summary: "script.doc.migrate",
		Args: []ArgDoc{
			{Name: "ХОСТ", Required: true, Desc: "script.doc.migrate.from"},
			{Name: "КОНТЕЙНЕР", Required: true, Desc: "script.doc.migrate.container"},
			{Name: "to ХОСТ", Required: true, Desc: "script.doc.migrate.to"},
			{Name: "switch-fly", Desc: "script.doc.migrate.switchFly"},
			{Name: "switch-pipeline", Desc: "script.doc.migrate.switchPipeline"},
			{Name: "clean-leftovers", Desc: "script.doc.migrate.cleanLeftovers"},
		},
		Example: "migrate web1 forgejo-forgejo-1 to web2 switch-fly switch-pipeline",
	},
	{
		Kind: KindK8sIngress, Syntax: "k8s КЛАСТЕР ingress cilium [port ПОРТ]",
		Summary: "script.doc.k8sIngress",
		Args: []ArgDoc{
			{Name: "КЛАСТЕР", Required: true, Desc: "script.doc.k8sCluster"},
			{Name: "port", Desc: "script.doc.k8sIngress.port"},
		},
		Example: "k8s lab ingress cilium port 8080",
	},
	{
		Kind: KindK8sApply, Syntax: "k8s КЛАСТЕР apply\n  манифест построчно\nend",
		Summary: "script.doc.k8sApply", Block: true,
		Args:    []ArgDoc{{Name: "КЛАСТЕР", Required: true, Desc: "script.doc.k8sCluster"}},
		Example: "k8s lab apply\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: demo\nend",
	},
	{
		Kind: KindK8sWait, Syntax: "k8s КЛАСТЕР wait deployment ИМЯ [namespace NS] [ДЛИТЕЛЬНОСТЬ]",
		Summary: "script.doc.k8sWait",
		Args: []ArgDoc{
			{Name: "КЛАСТЕР", Required: true, Desc: "script.doc.k8sCluster"},
			{Name: "ИМЯ", Required: true, Desc: "script.doc.k8sWait.name"},
			{Name: "namespace", Desc: "script.doc.k8sWait.namespace"},
			{Name: "ДЛИТЕЛЬНОСТЬ", Desc: "script.doc.wait.duration"},
		},
		Example: "k8s lab wait deployment hello 5m",
	},
	{
		Kind: KindFlyRoute, Syntax: "fly route ИД ВИД [names ИМЯ,…] [port ПОРТ] [tls gateway] [dns plugin] [check tcp|http|none] [path ПУТЬ] [gateway ШЛЮЗ] (targets cluster КЛАСТЕР port ПОРТ | target ХОСТ container|vm|lxd ИМЯ ПОРТ … | target ХОСТ host ПОРТ …)",
		Summary: "script.doc.flyRoute",
		Args: []ArgDoc{
			{Name: "ИД", Required: true, Desc: "script.doc.flyRoute.id"},
			{Name: "ВИД", Required: true, Desc: "script.doc.flyRoute.kind"},
			{Name: "names", Desc: "script.doc.flyRoute.names"},
			{Name: "port", Desc: "script.doc.flyRoute.port"},
			{Name: "tls gateway", Desc: "script.doc.flyRoute.tls"},
			{Name: "dns plugin", Desc: "script.doc.flyRoute.dns"},
			{Name: "check", Desc: "script.doc.flyRoute.check"},
			{Name: "gateway", Desc: "script.doc.flyRoute.gateway"},
			{Name: "targets cluster", Desc: "script.doc.flyRoute.cluster"},
			{Name: "target", Desc: "script.doc.flyRoute.target"},
		},
		Example: "fly route hello https names hello.example.com tls gateway check tcp targets cluster lab port 8080\nfly route mc tcp port 25565 target web1 container mc-server-1 25565",
	},
	{
		Kind: KindFlyApply, Syntax: "fly apply",
		Summary: "script.doc.flyApply",
		Example: "fly apply",
	},
	{
		Kind: KindFlyOwn, Syntax: "fly own ИМЯ machine МАШИНА port ПОРТ | fly own ИМЯ cluster КЛАСТЕР namespace NS",
		Summary: "script.doc.flyOwn",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.flyOwn.name"},
			{Name: "machine / port", Desc: "script.doc.flyOwn.machine"},
			{Name: "cluster / namespace", Desc: "script.doc.flyOwn.cluster"},
		},
		Example: "fly own shop.client.example.com cluster shared namespace shop",
	},
	{
		Kind: KindPlugin, Syntax: "plugin ИМЯ СЛОВО аргументы… | СЛОВО аргументы…",
		Summary: "script.doc.plugin",
		Args: []ArgDoc{
			{Name: "ИМЯ", Required: true, Desc: "script.doc.plugin.name"},
			{Name: "СЛОВО", Required: true, Desc: "script.doc.plugin.word"},
		},
		Example: "dns zone add example.com\ndns set app.example.com A 203.0.113.10 ttl 300\nplugin dns dns rm old.example.com",
	},
	{
		Kind: KindRun, OnHost: true, Block: true, Syntax: "on ХОСТ run \"КОМАНДА\" | on ХОСТ run … end",
		Summary: "script.doc.run",
		Args: []ArgDoc{
			{Name: "КОМАНДА", Desc: "script.doc.run.command"},
		},
		Example: "on router1 run \"/ip dns cache flush\"\non router1 run\n/ip firewall address-list add list=blocked address=198.51.100.7\n/ip firewall address-list print where list=blocked\nend",
	},
}

// DocFor — команда справки по виду шага (nil — нет такой).
func DocFor(k Kind) *CommandDoc { return docByKind(k) }

// docByKind — команда справки по виду шага.
func docByKind(k Kind) *CommandDoc {
	for i := range Commands {
		if Commands[i].Kind == k {
			return &Commands[i]
		}
	}
	return nil
}

// Example — готовый сценарий целиком для справки: скопировать или
// вставить в редактор и подправить.
type Example struct {
	// Title/Summary — ключи msgs.
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Content string `json:"content"`
}

// Examples — типовые сценарии от простого к сложному.
var Examples = []Example{
	{
		Title: "script.ex.newHost.title", Summary: "script.ex.newHost.summary",
		Content: `group prod

host web1 192.0.2.10 user root password ask group prod
install web1

on web1 packages install nginx htop ncdu
on web1 service nginx enable
on web1 firewall allow 80/tcp
on web1 firewall allow 443/tcp
`,
	},
	{
		Title: "script.ex.docker.title", Summary: "script.ex.docker.summary",
		Content: `host app1 192.0.2.20 user deploy password ask
install app1

on app1 docker install
on app1 docker stack /srv/app/docker-compose.yml up
services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
  db:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_PASSWORD: change-me
    volumes:
      - pgdata:/var/lib/postgresql/data
volumes:
  pgdata: {}
end
on app1 firewall allow 8080/tcp
`,
	},
	{
		Title: "script.ex.vms.title", Summary: "script.ex.vms.summary",
		Content: `set IMAGE ubuntu-24.04
group lab profile base

# hv1 уже есть в хабе — заводить не нужно
on hv1 vm create lab-web image ${IMAGE} cpu 2 mem 2048 disk 20 profile base install
on hv1 vm create lab-db image ${IMAGE} cpu 2 mem 4096 disk 40 install

on lab-db packages install postgresql
on lab-db firewall allow 5432 from 192.168.100.0/24
on lab-web packages install nginx
`,
	},
	{
		Title: "script.ex.forgejoFly.title", Summary: "script.ex.forgejoFly.summary",
		Content: ForgejoFlyExample,
	},
	{
		Title: "script.ex.k8sFirstPod.title", Summary: "script.ex.k8sFirstPod.summary",
		Content: K8sFirstPodExample,
	},
	{
		Title: "script.ex.maintain.title", Summary: "script.ex.maintain.summary",
		Content: `# Хосты уже есть в хабе: только действия
on web1 apply profile web-base
on web2 apply profile web-base

on web1 file put /etc/motd
This server is managed by nkt.
end
on web1 service nginx reload
on web2 packages remove telnet
`,
	},
}

// englishTerms — плейсхолдеры и служебные фразы синтаксиса и примеров
// по-английски. Таблица команд пишется один раз по-русски, а справка на
// английском получает те же строки через Localize; ключевые слова языка
// и одинаковы в обоих вариантах.
var englishTerms = [][2]string{
	{"КОМАНДА", "COMMAND"},
	{"compose-файл построчно", "compose file line by line"},
	{"описание конвейера построчно", "pipeline description line by line"},
	{"Forgejo за Traffic Fly: выкладка и перенос на другой хост", "Forgejo behind Traffic Fly: deploy and move to another host"},
	{"хост, где Forgejo будет выложен сначала", "host where Forgejo is deployed first"},
	{"хост, куда его перенести", "host to move it to"},
	{"имя Forgejo — A-запись на адрес шлюза Traffic Fly", "Forgejo name — A record at the Traffic Fly gateway"},
	{"Веб и git по SSH — через шлюз; сертификат выпускает шлюз", "Web and git over SSH go through the gateway; the gateway issues the certificate"},
	{"Проверка снаружи — через DNS и шлюз, как зайдёт пользователь", "Check from outside — via DNS and the gateway, as a user would"},
	{"Перенос: маршруты Traffic Fly и конвейер переключаются на новый хост", "Move: Traffic Fly routes and the pipeline switch to the new host"},
	{"манифест построчно", "manifest line by line"},
	{"Кластер Kubernetes: от нуля до первого пода", "A Kubernetes cluster: from zero to the first pod"},
	{"хост с control plane и первым worker", "host with the control plane and the first worker"},
	{"хост со вторым worker", "host with the second worker"},
	{"имя тестового сайта — A-запись на адрес шлюза Traffic Fly", "test site name — A record at the Traffic Fly gateway"},
	{"kubeadm: 1 control plane + 2 worker на двух хостах, WireGuard, Cilium без kube-proxy", "kubeadm: 1 control plane + 2 workers on two hosts, WireGuard, Cilium without kube-proxy"},
	{"Ingress Cilium: порт 8080 на каждом узле", "Cilium Ingress: port 8080 on every node"},
	{"Тестовый под, сервис и Ingress", "Test pod, service and Ingress"},
	{"Вход снаружи: шлюз Traffic Fly на все узлы кластера", "Outside access: the Traffic Fly gateway to all cluster nodes"},
	{"аргументы…", "args…"},
	{"СЛОВО", "WORD"},
	{"КОНВЕЙЕР", "PIPELINE"},
	{"КЛАСТЕР", "CLUSTER"},
	{"ШЛЮЗ", "GATEWAY"},
	{"ВИД", "KIND"},
	{"ИД", "ID"},
	{"КОНТЕЙНЕР", "CONTAINER"},
	{"содержимое построчно", "contents line by line"},
	{"адрес нового сервера", "address of the new server"},
	{"имя хоста в хабе", "host name on the hub"},
	{"Этот сервер под управлением nkt", "This server is managed by nkt"},
	{"hv1 уже есть в хабе — заводить не нужно", "hv1 is already on the hub — no need to register it"},
	{"Хосты уже есть в хабе: только действия", "The hosts are already on the hub: actions only"},
	{"ПОЛЬЗОВАТЕЛЬ", "USER"},
	{"ДЛИТЕЛЬНОСТЬ", "DURATION"},
	{"подсказка", "prompt"},
	{"действие", "action"},
	{"значение", "value"},
	{"ПРОФИЛЬ", "PROFILE"},
	{"КАТАЛОГ", "DIR"},
	{"ГРУППА", "GROUP"},
	{"ЛОКАЛЬ", "LOCALE"},
	{"СЕРВЕР", "SERVER"},
	{"ПАКЕТ", "PACKAGE"},
	{"АДРЕС", "ADDR"},
	{"СНИМОК", "SNAPSHOT"},
	{"МАШИНА", "MACHINE"},
	{"ОБРАЗ", "IMAGE"},
	{"МОСТ", "BRIDGE"},
	{"ДОМЕН", "DOMAIN"},
	{"ВЕТКА", "BRANCH"},
	{"ХОСТ", "HOST"},
	{"ПОРТ", "PORT"},
	{"ВЕРСИЯ", "VERSION"},
	{"ПУТЬ", "PATH"},
	{"СЕТЬ", "NETWORK"},
	{"ЗОНА", "ZONE"},
	{"ИМЯ", "NAME"},
	{"КОД", "CODE"},
	{"МБ", "MB"},
	{"ГБ", "GB"},
}

// Localize возвращает строку синтаксиса или примера на английском, если
// en; иначе — как есть.
func Localize(text string, en bool) string {
	if !en {
		return text
	}
	for _, t := range englishTerms {
		text = strings.ReplaceAll(text, t[0], t[1])
	}
	return text
}

// ForgejoFlyExample — сценарий справки «Перенос стека с Traffic Fly»:
// Forgejo через шлюз (examples/forgejo/pipeline.fly.yaml), выкладка,
// проверка снаружи, перенос на другой хост и проверка снова.
const ForgejoFlyExample = `# Forgejo за Traffic Fly: выкладка и перенос на другой хост
param FROM "хост, где Forgejo будет выложен сначала"
param TO "хост, куда его перенести"
param DOMAIN "имя Forgejo — A-запись на адрес шлюза Traffic Fly"

# Веб и git по SSH — через шлюз; сертификат выпускает шлюз
pipeline forgejo
repo: https://github.com/piqab/nkt.git
ref: main
action: compose
compose:
  file: examples/forgejo/deploy/docker-compose.yml
  project: forgejo
  hosts: [${FROM}]
  wait_timeout: 5m
  env_keys:
    forgejo-admin: [FORGEJO_ADMIN_USER, FORGEJO_ADMIN_PASSWORD, FORGEJO_ADMIN_EMAIL]
  env:
    POSTGRES_PASSWORD: generate:password
    FORGEJO_DOMAIN: ${DOMAIN}
    FORGEJO_ROOT_URL: https://${DOMAIN}/
    FORGEJO_ADMIN_USER: gitadmin
    FORGEJO_ADMIN_PASSWORD: generate:password
    FORGEJO_ADMIN_EMAIL: admin@example.com
  fly:
    - route: forgejo-web
      kind: https
      names: [${DOMAIN}]
      tls: gateway
      service: forgejo
      target_port: 3000
      check: http
      check_path: /api/healthz
    - route: forgejo-ssh
      kind: tcp
      port: 2222
      service: forgejo
      target_port: 2222
end
deploy forgejo 20m

# Проверка снаружи — через DNS и шлюз, как зайдёт пользователь
wait url https://${DOMAIN}/api/healthz 200 10m

# Перенос: маршруты Traffic Fly и конвейер переключаются на новый хост
migrate ${FROM} forgejo-forgejo-1 to ${TO} switch-fly switch-pipeline
wait url https://${DOMAIN}/api/healthz 200 5m
`

// K8sFirstPodExample — сценарий справки «Кластер Kubernetes: от нуля до
// первого пода»: кластер kubeadm на двух хостах, Ingress Cilium, тестовый
// под и вход снаружи через Traffic Fly.
const K8sFirstPodExample = `# Кластер Kubernetes: от нуля до первого пода
param HOST1 "хост с control plane и первым worker"
param HOST2 "хост со вторым worker"
param DOMAIN "имя тестового сайта — A-запись на адрес шлюза Traffic Fly"

# kubeadm: 1 control plane + 2 worker на двух хостах, WireGuard, Cilium без kube-proxy
on ${HOST1} k8s create lab flavor kubeadm nodes "${HOST1}: cp 1, w 1; ${HOST2}: w 1" network wireguard cni cilium no-kube-proxy image debian-13 expose http 0 https 0

# Ingress Cilium: порт 8080 на каждом узле
k8s lab ingress cilium port 8080

# Тестовый под, сервис и Ingress
k8s lab apply
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello
spec:
  replicas: 2
  selector:
    matchLabels: {app: hello}
  template:
    metadata:
      labels: {app: hello}
    spec:
      containers:
        - name: whoami
          image: traefik/whoami:v1.12.0
          ports: [{containerPort: 80}]
---
apiVersion: v1
kind: Service
metadata:
  name: hello
spec:
  selector: {app: hello}
  ports: [{port: 80, targetPort: 80}]
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: hello
spec:
  ingressClassName: cilium
  rules:
    - host: ${DOMAIN}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service: {name: hello, port: {number: 80}}
end
k8s lab wait deployment hello 5m

# Вход снаружи: шлюз Traffic Fly на все узлы кластера
fly route hello https names ${DOMAIN} tls gateway check tcp targets cluster lab port 8080
wait url https://${DOMAIN}/ 200 10m
`
