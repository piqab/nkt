package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/k8s"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Кластер Kubernetes на виртуалках хоста: хаб создаёт машины (тем же
// заданием, что и «Новая машина», с установкой nkt), ставит на первую
// control plane, берёт у неё токен, присоединяет остальные, ждёт Ready,
// при желании пробрасывает порты хоста в control plane и забирает
// kubeconfig. Узлы — обычные хосты в группе с именем кластера.

const (
	KindClusterCreate = "cluster.create"
	KindClusterDelete = "cluster.delete"
	KindClusterAdd    = "cluster.add"

	TopologySingle = "single" // одна машина: control plane + worker
	TopologyCP1    = "cp1"    // 1 control plane + N worker
	TopologyCP3    = "cp3"    // 3 control plane + N worker

	clusterReadyTimeout = 15 * time.Minute
	clusterNodePoll     = 10 * time.Second
)

// ClusterSpec — что создаём. Хранится в clusters.spec_json.
type ClusterSpec struct {
	Name     string `json:"name"`
	HostID   int64  `json:"host_id"`
	Flavor   string `json:"flavor"`
	Topology string `json:"topology"`
	Workers  int    `json:"workers"`
	Expose   bool   `json:"expose"`
	// Порты хоста при пробросе: API (→6443), HTTP (→80), HTTPS (→443);
	// 0 — не пробрасывать этот. Пустые при Expose — умолчания.
	ExposeAPI   int `json:"expose_api,omitempty"`
	ExposeHTTP  int `json:"expose_http,omitempty"`
	ExposeHTTPS int `json:"expose_https,omitempty"`
	// Endpoint — общий адрес control plane: «host» или «host:port» (порт
	// по умолчанию — 6443). Только для kubeadm: это его
	// --control-plane-endpoint, по которому узлы входят и по которому
	// отдаётся kubeconfig. При трёх control plane он обязателен: без
	// общего адреса API кластера висит на первом узле, и вход остальных
	// двух ничего не меняет. Имя endpoint'а kubeadm сам кладёт в
	// сертификат API каждого control plane.
	Endpoint string `json:"endpoint,omitempty"`
	// Gateway — шлюз Traffic Fly, на котором слушает этот адрес (edge id).
	// Пусто — адрес выбирает шлюз сам: указал бы он на несколько, хаб не
	// угадывает, а молча выбранный шлюз означал бы адрес кластера не там,
	// где его ждут.
	Gateway int64 `json:"gateway,omitempty"`
	// AllowExtra — кому ещё, кроме узлов кластера, разрешён вход на адрес
	// endpoint'а (адреса и сети IPv4). Слушатель наружу — белый список
	// адресов клиентов: без него кластер торчал бы открытым портом всему
	// интернету.
	AllowExtra []string `json:"allow_extra,omitempty"`
	// CNI — «cilium» или пусто (flannel); KubeProxyReplacement — Cilium
	// вместо kube-proxy.
	CNI                  string `json:"cni,omitempty"`
	KubeProxyReplacement bool   `json:"kube_proxy_replacement,omitempty"`
	// CiliumValues — Helm values для Cilium (YAML). Пусто — значения
	// по умолчанию: cni.binPath=/usr/lib/cni, kubeProxyReplacement=true
	// (если включён в форме). Передаётся в cilium install через --values.
	CiliumValues string `json:"cilium_values,omitempty"`
	// K8sVersion — kubeadm: минорная версия («1.34»). Пусто — хаб
	// подбирает актуальную stable при создании и записывает сюда, чтобы
	// все узлы (и добавленные потом worker'ы) были одной версии.
	K8sVersion string `json:"k8s_version,omitempty"`
	// Placements — размещение по хостам (раздел «Кластеры»); пусто —
	// один хост из HostID/Topology/Workers (диалог у хоста).
	Placements []Placement `json:"placements,omitempty"`
	// NetworkMode — как узлы на разных хостах видят друг друга: nat (один
	// хост, сеть libvirt), bridge (машины в сети хостов), macvtap (машина
	// включена в устройство хоста напрямую — в той же физической сети,
	// что и хосты), wireguard (туннели между хостами).
	NetworkMode string `json:"network_mode,omitempty"`
	ImageID     string `json:"image_id"`
	Network     string `json:"network,omitempty"`
	User        string `json:"user"`
	// Размеры control plane и worker'ов.
	CPVCPUs    int `json:"cp_vcpus"`
	CPMemoryMB int `json:"cp_memory_mb"`
	CPDiskGB   int `json:"cp_disk_gb"`
	WVCPUs     int `json:"w_vcpus"`
	WMemoryMB  int `json:"w_memory_mb"`
	WDiskGB    int `json:"w_disk_gb"`
	// Addons — дополнения поверх готового кластера (Longhorn): ставятся
	// последним шагом создания и кнопкой «Дополнения» у кластера.
	Addons []ClusterAddon `json:"addons,omitempty"`
	// LBEnabled — встроенный балансировщик: 1–2 машины с HAProxy (+ keepalived
	// при HA-паре). Endpoint кластера = VIP (HA) или адрес lb-1.
	LBEnabled bool `json:"lb_enabled,omitempty"`
	// LBCount — количество машин балансировщика (1 или 2).
	LBCount int `json:"lb_count,omitempty"`
	// LBImage — образ машин балансировщика (ID образа из библиотеки хаба
	// или имя хостового образа с префиксом "host:").
	LBImage string `json:"lb_image,omitempty"`
	// VIP — виртуальный адрес HA-пары балансировщика (keepalived). Обязателен
	// при LBCount=2, опционален при 1 (тогда endpoint = адрес lb-1).
	VIP string `json:"vip,omitempty"`
	// LBPlacements — размещение машин балансировщика по хостам.
	LBPlacements []Placement `json:"lb_placements,omitempty"`
	// LBHaproxy — параметры HAProxy (таймауты, maxconn, порт статистики,
	// алгоритм балансировки). Пусто — значения по умолчанию.
	LBHaproxy LBHaproxyConfig `json:"lb_haproxy,omitempty"`
}

// LBHaproxyConfig — параметры HAProxy для балансировщика кластера.
type LBHaproxyConfig struct {
	// ConnectTimeout — таймаут подключения к бэкенду (мс).
	ConnectTimeout int `json:"connect_timeout,omitempty"`
	// ClientTimeout — таймаут клиента (мс).
	ClientTimeout int `json:"client_timeout,omitempty"`
	// ServerTimeout — таймаут сервера (мс).
	ServerTimeout int `json:"server_timeout,omitempty"`
	// MaxConn — максимальное число соединений.
	MaxConn int `json:"max_conn,omitempty"`
	// StatsPort — порт веб-интерфейса статистики HAProxy (0 = отключить).
	StatsPort int `json:"stats_port,omitempty"`
	// Algorithm — алгоритм балансировки: "source-hash" (API) или
	// "round-robin" (ingress).
	Algorithm string `json:"algorithm,omitempty"`
}

// ClusterAddon — дополнение и версия его чарта (пусто — версия nkt по
// умолчанию; при создании записывается выбранная, чтобы продолжение и
// новые узлы шли той же).
type ClusterAddon struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Placement — строка размещения: на хосте — N машин с ролью или сам хост.
type Placement struct {
	HostID int64  `json:"host_id"`
	Role   string `json:"role"` // control-plane | worker
	Kind   string `json:"kind"` // vm | host
	Count  int    `json:"count"`
	VCPUs  int    `json:"vcpus,omitempty"`
	MemMB  int    `json:"memory_mb,omitempty"`
	DiskGB int    `json:"disk_gb,omitempty"`
	// ImageID/Network — для машин; Bridge — мост хоста в режиме bridge;
	// Nic — устройство хоста, в которое включается машина в режиме
	// macvtap (у каждого хоста оно своё).
	ImageID string `json:"image_id,omitempty"`
	Network string `json:"network,omitempty"`
	Bridge  string `json:"bridge,omitempty"`
	Nic     string `json:"nic,omitempty"`
	// Endpoint — адрес этого хоста для соседей по туннелю (режим
	// wireguard); пусто — адрес хоста из списка nkt.
	Endpoint string `json:"endpoint,omitempty"`
}

const (
	NetworkNAT       = "nat"
	NetworkBridge    = "bridge"
	NetworkMacvtap   = "macvtap"
	NetworkWireGuard = "wireguard"

	RoleControlPlane = "control-plane"
	RoleWorker       = "worker"
	RoleLoadBalancer = "load-balancer"
	KindVM           = "vm"
	KindHost         = "host"
)

// clusterNode — один узел после разворачивания размещения.
type clusterNode struct {
	Name    string
	HostID  int64 // хост, на котором машина (или сам узел при Kind=host)
	Role    string
	Kind    string
	VCPUs   int
	MemMB   int
	DiskGB  int
	ImageID string
	Network string
	Bridge  string
	// Nic — устройство хоста для macvtap: машина включается в него
	// напрямую, и на каждом хосте оно своё.
	Nic string
}

// k8sNodeHosts filters cluster host records down to actual Kubernetes
// nodes. Load-balancer machines are attached to the cluster record
// (k8s_role = load-balancer) but run no k8s flavor: their agents reject
// node jobs (addon prepare, upgrade) and they must not be counted when
// sizing replicas or steps. ClusterHosts sorts control-plane first and
// the filter preserves order, so nodes[0] stays the control plane.
func k8sNodeHosts(hosts []store.Host) []store.Host {
	out := make([]store.Host, 0, len(hosts))
	for _, h := range hosts {
		if h.K8sRole != RoleLoadBalancer {
			out = append(out, h)
		}
	}
	return out
}

var k8sMinorRe = regexp.MustCompile(`^\d+\.\d+$`)

// nicNameRe — имя устройства хоста: форма та же, что у моста и сети
// libvirt (имя уходит в команду ip и в XML домена).
var nicNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// hostnameLikeRe — имя кластера: оно же префикс имён машин и hostname.
var hostnameLikeRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// Validate проверяет форму и заполняет умолчания.
func (s *ClusterSpec) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	if !hostnameLikeRe.MatchString(s.Name) {
		return msgs.Errorf("hub.clusterBadName", s.Name)
	}
	if s.Flavor != k8s.FlavorK3s && s.Flavor != k8s.FlavorKubeadm {
		return msgs.Errorf("k8s.badFlavor", s.Flavor)
	}
	s.K8sVersion = strings.TrimPrefix(strings.TrimSpace(s.K8sVersion), "v")
	if s.K8sVersion != "" && !k8sMinorRe.MatchString(s.K8sVersion) {
		return msgs.Errorf("hub.clusterBadK8sVersion", s.K8sVersion)
	}
	if err := s.normalizeEndpoint(); err != nil {
		return err
	}
	if err := s.normalizeListener(); err != nil {
		return err
	}
	if len(s.Placements) > 0 {
		if err := s.validatePlacements(); err != nil {
			return err
		}
	} else {
		switch s.Topology {
		case TopologySingle:
			s.Workers = 0
		case TopologyCP1:
			if s.Workers < 1 {
				return msgs.Errorf("hub.clusterNeedsWorkers")
			}
		case TopologyCP3:
			// При LB endpoint будет установлен после шага lb — не требуем его здесь.
			if s.Flavor == k8s.FlavorKubeadm && s.Endpoint == "" && !s.LBEnabled {
				return msgs.Errorf("hub.clusterEndpointNeeded")
			}
		default:
			return msgs.Errorf("hub.clusterBadTopology", s.Topology)
		}
		if s.Workers > 20 {
			return msgs.Errorf("hub.clusterTooManyWorkers")
		}
		if s.ImageID == "" {
			return msgs.Errorf("hub.clusterNeedsImage")
		}
		if s.NetworkMode == "" {
			s.NetworkMode = NetworkNAT
		}
		if s.NetworkMode == NetworkMacvtap {
			// Устройство для macvtap выбирается по хостам: в одном
			// размещении «на весь кластер» его назвать негде — на каждом
			// хосте оно своё.
			return msgs.Errorf("hub.clusterMacvtapNeedsPlacements")
		}
	}
	if s.CNI != "" && s.CNI != "cilium" {
		return msgs.Errorf("k8s.badCNI", s.CNI)
	}
	if err := validateAddons(s.Addons, s.Flavor); err != nil {
		return err
	}
	if s.LBEnabled {
		if err := s.validateLB(); err != nil {
			return err
		}
	}
	if s.CNI != "cilium" {
		s.KubeProxyReplacement = false
	}
	if s.Expose {
		if s.ExposeAPI == 0 && s.ExposeHTTP == 0 && s.ExposeHTTPS == 0 {
			s.ExposeAPI, s.ExposeHTTP, s.ExposeHTTPS = 6443, 80, 443
		}
		for _, p := range []int{s.ExposeAPI, s.ExposeHTTP, s.ExposeHTTPS} {
			if p < 0 || p > 65535 {
				return msgs.Errorf("hub.clusterBadPort", p)
			}
		}
		if s.ExposeAPI == 0 {
			// Без API-порта kubeconfig наружу не собрать — проброс без
			// него бессмыслен.
			return msgs.Errorf("hub.clusterExposeNeedsAPI")
		}
	} else {
		s.ExposeAPI, s.ExposeHTTP, s.ExposeHTTPS = 0, 0, 0
	}
	if s.User == "" {
		s.User = "deploy"
	}
	if s.CPVCPUs == 0 {
		s.CPVCPUs = 2
	}
	if s.CPMemoryMB == 0 {
		s.CPMemoryMB = 4096
	}
	if s.CPDiskGB == 0 {
		s.CPDiskGB = 30
	}
	if s.WVCPUs == 0 {
		s.WVCPUs = 2
	}
	if s.WMemoryMB == 0 {
		s.WMemoryMB = 4096
	}
	if s.WDiskGB == 0 {
		s.WDiskGB = 30
	}
	return nil
}

// normalizeEndpoint приводит общий адрес control plane к форме
// «host:port». Пустое поле остаётся пустым: требовать его право
// проверке топологии, а не этой функции. Поле имеет смысл только у
// kubeadm — у k3s общей точки входа нет, узлы входят по адресу
// конкретного server'а, и молча проигнорированное значение выглядело
// бы сделанным.
func (s *ClusterSpec) normalizeEndpoint() error {
	s.Endpoint = strings.TrimSpace(s.Endpoint)
	if s.Endpoint == "" {
		return nil
	}
	if s.Flavor != k8s.FlavorKubeadm {
		return msgs.Errorf("hub.clusterEndpointKubeadmOnly")
	}
	if _, _, err := k8s.EndpointHostPort(s.Endpoint); err != nil {
		return err
	}
	// Порт дописывается сразу: им оперируют и join'ы узлов, и
	// «--set k8sServicePort» у Cilium, и слушатель наружу.
	s.Endpoint = k8s.JoinServer(s.Endpoint)
	return nil
}

// normalizeListener приводит поля слушателя общего адреса. Сам слушатель
// строится заданием кластера (см. cluster_listener.go), здесь проверяется
// форма: выбор шлюза без общего адреса означал бы, что поле заполнили, а
// слушать нечего, и адрес из «кому ещё» — только тот, который понимает
// белый список Traffic Fly (IPv4 и сети).
func (s *ClusterSpec) normalizeListener() error {
	var out []string
	seen := map[string]bool{}
	for _, a := range s.AllowExtra {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if !fly.ValidAllow(a) {
			return msgs.Errorf("hub.clusterBadAllow", a)
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	s.AllowExtra = out
	if len(s.AllowExtra) > fly.MaxAllow {
		return msgs.Errorf("hub.clusterTooManyAllows", len(s.AllowExtra), fly.MaxAllow)
	}
	if s.Endpoint == "" && (s.Gateway != 0 || len(s.AllowExtra) > 0) {
		return msgs.Errorf("hub.clusterListenerNeedsEndpoint")
	}
	return nil
}

// validatePlacements проверяет размещение по хостам.
func (s *ClusterSpec) validatePlacements() error {
	cps, total := 0, 0
	hosts := map[int64]bool{}
	for i := range s.Placements {
		pl := &s.Placements[i]
		if pl.HostID <= 0 {
			return msgs.Errorf("hub.clusterPlacementHost")
		}
		if pl.Role != RoleControlPlane && pl.Role != RoleWorker {
			return msgs.Errorf("hub.clusterPlacementRole", pl.Role)
		}
		switch pl.Kind {
		case KindHost:
			pl.Count = 1
		case KindVM:
			if pl.Count < 1 || pl.Count > 20 {
				return msgs.Errorf("hub.clusterTooManyWorkers")
			}
			if pl.ImageID == "" {
				pl.ImageID = s.ImageID
			}
			if pl.ImageID == "" {
				return msgs.Errorf("hub.clusterNeedsImage")
			}
			if pl.VCPUs == 0 {
				pl.VCPUs = 2
			}
			if pl.MemMB == 0 {
				pl.MemMB = 4096
			}
			if pl.DiskGB == 0 {
				pl.DiskGB = 30
			}
		default:
			return msgs.Errorf("hub.clusterPlacementKind", pl.Kind)
		}
		if pl.Role == RoleControlPlane {
			cps += pl.Count
		}
		total += pl.Count
		hosts[pl.HostID] = true
	}
	if cps != 1 && cps != 3 {
		return msgs.Errorf("hub.clusterCPCount", cps)
	}
	// При LB endpoint будет установлен после шага lb — не требуем его здесь.
	if cps == 3 && s.Flavor == k8s.FlavorKubeadm && s.Endpoint == "" && !s.LBEnabled {
		return msgs.Errorf("hub.clusterEndpointNeeded")
	}
	if total < 1 {
		return msgs.Errorf("hub.clusterNoNodes")
	}
	if s.NetworkMode == "" {
		s.NetworkMode = NetworkNAT
	}
	switch s.NetworkMode {
	case NetworkNAT:
		if len(hosts) > 1 {
			return msgs.Errorf("hub.clusterNATSingleHost")
		}
	case NetworkBridge:
		for _, pl := range s.Placements {
			if pl.Kind == KindVM && pl.Bridge == "" {
				return msgs.Errorf("hub.clusterBridgeNeeded")
			}
		}
	case NetworkMacvtap:
		// Машина включается в устройство хоста напрямую — мост и сеть
		// libvirt при этом ни при чём. Устройство обязано быть названо для
		// каждой машины: на каждом хосте своё, и угадать его хаб не может.
		for i := range s.Placements {
			pl := &s.Placements[i]
			pl.Bridge, pl.Network = "", ""
			pl.Nic = strings.TrimSpace(pl.Nic)
			if pl.Kind == KindHost {
				pl.Nic = ""
				continue
			}
			if pl.Nic == "" {
				return msgs.Errorf("hub.clusterNicNeeded")
			}
			if !nicNameRe.MatchString(pl.Nic) {
				return msgs.Errorf("hub.clusterBadNic", pl.Nic)
			}
		}
		// Проброс с хоста на его собственную macvtap-гостью не делается:
		// хост с ней не говорит (ограничение macvlan), поэтому DNAT на
		// адрес узла не сработает и «проброс наружу» дал бы зелёный шаг с
		// мёртвым адресом. Общий адрес кластера при этом остаётся — его
		// обслуживает шлюз, а не хост размещения.
		if s.Expose {
			return msgs.Errorf("hub.clusterExposeMacvtap")
		}
	case NetworkWireGuard:
		// Сеть машин на каждом хосте заводится своя (см. cluster_wg.go),
		// мост и выбранная сеть не нужны.
		for i := range s.Placements {
			pl := &s.Placements[i]
			pl.Bridge, pl.Network = "", ""
			pl.Endpoint = strings.TrimSpace(pl.Endpoint)
			if pl.Endpoint != "" && (strings.ContainsAny(pl.Endpoint, " \t\n'\"`$\\;&|/") || strings.Contains(pl.Endpoint, ":")) {
				return msgs.Errorf("hub.clusterBadEndpoint", pl.Endpoint)
			}
		}
	default:
		return msgs.Errorf("hub.clusterBadNetwork", s.NetworkMode)
	}
	// HostID — хост первого control plane: туда идут проброс и адрес
	// kubeconfig.
	for _, pl := range s.Placements {
		if pl.Role == RoleControlPlane {
			s.HostID = pl.HostID
			break
		}
	}
	s.Topology = ""
	return nil
}

// validateLB проверяет размещение машин балансировщика.
func (s *ClusterSpec) validateLB() error {
	if s.LBCount != 1 && s.LBCount != 2 {
		return msgs.Errorf("hub.clusterLBCount", s.LBCount)
	}
	if s.LBCount == 2 && s.VIP == "" {
		return msgs.Errorf("hub.clusterLBNeedsVIP")
	}
	if len(s.LBPlacements) == 0 {
		return msgs.Errorf("hub.clusterLBNeedsPlacements")
	}
	for i := range s.LBPlacements {
		pl := &s.LBPlacements[i]
		if pl.HostID <= 0 {
			return msgs.Errorf("hub.clusterPlacementHost")
		}
		if pl.Kind != KindVM {
			return msgs.Errorf("hub.clusterLBKind", pl.Kind)
		}
		if pl.Count < 1 || pl.Count > 2 {
			return msgs.Errorf("hub.clusterLBCountRange", pl.Count)
		}
		if pl.ImageID == "" {
			pl.ImageID = s.LBImage
		}
		if pl.ImageID == "" {
			return msgs.Errorf("hub.clusterLBNeedsImage")
		}
		if pl.VCPUs == 0 {
			pl.VCPUs = 1
		}
		if pl.MemMB == 0 {
			pl.MemMB = 1024
		}
		if pl.DiskGB == 0 {
			pl.DiskGB = 10
		}
		// В режиме macvtap устройство обязательно (как у cp/w).
		if s.NetworkMode == NetworkMacvtap && pl.Nic == "" {
			return msgs.Errorf("hub.clusterNicNeeded")
		}
		if s.NetworkMode == NetworkMacvtap && !nicNameRe.MatchString(pl.Nic) {
			return msgs.Errorf("hub.clusterBadNic", pl.Nic)
		}
	}
	// VIP должен быть валидным IP.
	if s.VIP != "" {
		if _, err := netip.ParseAddr(s.VIP); err != nil {
			return msgs.Errorf("hub.clusterBadVIP", s.VIP)
		}
	}
	// Параметры HAProxy — проверка диапазонов.
	if s.LBHaproxy.ConnectTimeout < 0 || s.LBHaproxy.ConnectTimeout > 60000 {
		return msgs.Errorf("hub.clusterLBHaproxyTimeout", "connect", s.LBHaproxy.ConnectTimeout)
	}
	if s.LBHaproxy.ClientTimeout < 0 || s.LBHaproxy.ClientTimeout > 300000 {
		return msgs.Errorf("hub.clusterLBHaproxyTimeout", "client", s.LBHaproxy.ClientTimeout)
	}
	if s.LBHaproxy.ServerTimeout < 0 || s.LBHaproxy.ServerTimeout > 300000 {
		return msgs.Errorf("hub.clusterLBHaproxyTimeout", "server", s.LBHaproxy.ServerTimeout)
	}
	if s.LBHaproxy.MaxConn < 0 || s.LBHaproxy.MaxConn > 100000 {
		return msgs.Errorf("hub.clusterLBHaproxyMaxConn", s.LBHaproxy.MaxConn)
	}
	if s.LBHaproxy.StatsPort < 0 || s.LBHaproxy.StatsPort > 65535 {
		return msgs.Errorf("hub.clusterLBHaproxyStatsPort", s.LBHaproxy.StatsPort)
	}
	if s.LBHaproxy.Algorithm != "" && s.LBHaproxy.Algorithm != "source" && s.LBHaproxy.Algorithm != "roundrobin" {
		return msgs.Errorf("hub.clusterLBHaproxyAlgorithm", s.LBHaproxy.Algorithm)
	}
	return nil
}

// nodes разворачивает размещение в узлы с именами <кластер>-lb-N,
// <кластер>-cp-N, <кластер>-w-N; узел-хост носит имя самого хоста.
func (s ClusterSpec) nodes(hostName func(int64) string) []clusterNode {
	pls := s.Placements
	if len(pls) == 0 {
		pls = []Placement{
			{HostID: s.HostID, Role: RoleControlPlane, Kind: KindVM, Count: s.ControlPlanes(), VCPUs: s.CPVCPUs, MemMB: s.CPMemoryMB, DiskGB: s.CPDiskGB, ImageID: s.ImageID, Network: s.Network},
			{HostID: s.HostID, Role: RoleWorker, Kind: KindVM, Count: s.Workers, VCPUs: s.WVCPUs, MemMB: s.WMemoryMB, DiskGB: s.WDiskGB, ImageID: s.ImageID, Network: s.Network},
		}
	}
	var out []clusterNode
	lb, cp, w := 0, 0, 0
	// Сначала балансировщики, потом control plane, потом worker'ы — порядок
	// установки: LB должны быть готовы до cp1 (endpoint).
	for _, role := range []string{RoleLoadBalancer, RoleControlPlane, RoleWorker} {
		// Для LB используем отдельное размещение LBPlacements.
		var rolePlacements []Placement
		if role == RoleLoadBalancer {
			rolePlacements = s.LBPlacements
		} else {
			rolePlacements = pls
		}
		for _, pl := range rolePlacements {
			if pl.Role != role {
				continue
			}
			for i := 0; i < pl.Count; i++ {
				n := clusterNode{HostID: pl.HostID, Role: pl.Role, Kind: pl.Kind, VCPUs: pl.VCPUs, MemMB: pl.MemMB, DiskGB: pl.DiskGB,
					ImageID: pl.ImageID, Network: pl.Network, Bridge: pl.Bridge, Nic: pl.Nic}
				if pl.Kind == KindHost {
					n.Name = hostName(pl.HostID)
				} else if role == RoleLoadBalancer {
					lb++
					n.Name = fmt.Sprintf("%s-lb-%d", s.Name, lb)
				} else if role == RoleControlPlane {
					cp++
					n.Name = fmt.Sprintf("%s-cp-%d", s.Name, cp)
				} else {
					w++
					n.Name = fmt.Sprintf("%s-w-%d", s.Name, w)
				}
				out = append(out, n)
			}
		}
	}
	return out
}

// wgEndpoint — адрес хоста для соседей по туннелю: из размещения или
// адрес хоста.
func (s ClusterSpec) wgEndpoint(h store.Host) string {
	for _, pl := range s.Placements {
		if pl.HostID == h.ID && pl.Endpoint != "" {
			return pl.Endpoint
		}
	}
	return h.Addr
}

// exposeRules — правила проброса хоста в control plane.
func (s ClusterSpec) exposeRules() []map[string]int {
	var out []map[string]int
	for _, pr := range [][2]int{{s.ExposeAPI, 6443}, {s.ExposeHTTP, 80}, {s.ExposeHTTPS, 443}} {
		if pr[0] > 0 {
			out = append(out, map[string]int{"host_port": pr[0], "vm_port": pr[1]})
		}
	}
	return out
}

// APIAddr — адрес API в kubeconfig: общий endpoint, если он задан, —
// именно он адрес кластера, и в SAN каждого control plane; иначе хост с
// портом при пробросе, иначе первый control plane.
func (s ClusterSpec) apiAddr(hostAddr, cpAddr string) string {
	if s.Endpoint != "" {
		return s.Endpoint
	}
	if s.Expose && hostAddr != "" {
		return fmt.Sprintf("%s:%d", hostAddr, s.ExposeAPI)
	}
	return cpAddr + ":6443"
}

// ControlPlanes — сколько control plane в топологии.
func (s ClusterSpec) ControlPlanes() int {
	if s.Topology == TopologyCP3 {
		return 3
	}
	return 1
}

// NodeCounts — сколько control plane и worker'ов, с учётом размещения.
func (s ClusterSpec) NodeCounts() (cps, workers int) {
	if len(s.Placements) == 0 {
		return s.ControlPlanes(), s.Workers
	}
	for _, pl := range s.Placements {
		if pl.Role == RoleControlPlane {
			cps += pl.Count
		} else {
			workers += pl.Count
		}
	}
	return
}

// jobSteps — шагов в задании создания: проверки, узлы, control plane,
// присоединение, Ready; в режиме wireguard ещё туннель, при общем адресе —
// слушатель на шлюзе Traffic Fly.
func (s ClusterSpec) jobSteps() int {
	cps, workers := s.NodeCounts()
	n := cps + workers + 4
	if s.NetworkMode == NetworkWireGuard {
		n++
	}
	if s.Endpoint != "" {
		n++
	}
	return n
}

// nodeNames — имена машин: <кластер>-cp-N и <кластер>-w-N.
func (s ClusterSpec) nodeNames() (cps, workers []string) {
	for i := 1; i <= s.ControlPlanes(); i++ {
		cps = append(cps, fmt.Sprintf("%s-cp-%d", s.Name, i))
	}
	for i := 1; i <= s.Workers; i++ {
		workers = append(workers, fmt.Sprintf("%s-w-%d", s.Name, i))
	}
	return
}

// ClusterJobParams — вход заданий кластера.
type ClusterJobParams struct {
	ClusterID int64 `json:"cluster_id"`
	// AddWorkers — для cluster.add: сколько worker'ов добавить.
	AddWorkers int `json:"add_workers,omitempty"`
	// DryRun — только проверки (и подготовка при Prepare); Spec — вместо
	// записи кластера, которой в сухом прогоне не заводится.
	DryRun  bool         `json:"dry_run,omitempty"`
	Prepare bool         `json:"prepare,omitempty"`
	Spec    *ClusterSpec `json:"spec,omitempty"`
	// Steps — явный выбор оператора из окна шагов: исполнять ровно эти шаги
	// (см. cluster_steps.go). nil — прежний режим, где пропуск решает Resume.
	// Отмеченный и уже выполненный разрушительный шаг (cp1, join) идёт с
	// принудительным сбросом узла.
	Steps []string `json:"steps,omitempty"`
}

type clusterResume struct {
	// MeshUp — туннель между хостами уже поднят (режим wireguard).
	MeshUp bool `json:"mesh_up,omitempty"`
	// Hosts — заведённые машины по имени → id хоста.
	Hosts map[string]int64 `json:"hosts"`
	// Installed — узлы, на которых роль уже стоит.
	Installed map[string]bool `json:"installed"`
	// Listener — слушатель общего адреса разложен хабом. Отдельная отметка
	// нужна потому, что «cp-1 установлен» слушатель не доказывает в обратную
	// сторону: init мог упасть после слушателя, и окно шагов обязано видеть,
	// что повтор cp1 не требует разкладывать его заново.
	Listener bool `json:"listener,omitempty"`
	// LB — балансировщик (HAProxy + keepalived) установлен на машинах lb-*.
	LB bool `json:"lb,omitempty"`
	// Cilium — CNI установлен на cp-1 и раскатан по узлам.
	Cilium  bool `json:"cilium,omitempty"`
	Exposed bool `json:"exposed"`
	// Addons — пройденные шаги дополнений: «имя:prepare:узел», «имя:install».
	Addons map[string]bool `json:"addons,omitempty"`
}

// ClusterRunner — создание и пополнение кластера.
type ClusterRunner struct {
	m *Manager
	s *Server
}

// nodeIPAdvertise — адрес, который узел обязан рекламровать соседям, либо
// пусто, если узел пусть берёт свой сам. Два случая, когда адрес называет
// хаб: узел — сам хост в туннеле WireGuard (его адрес наружу соседи не
// видят, а видят адрес в туннеле) и машина, включённая в устройство хоста
// (сети libvirt с её DHCP и DNS здесь нет, и kubelet иначе возьмёт адрес,
// который соседи не видят, — ровно тот отказ, где etcd не может достучаться
// до соседа).
func nodeIPAdvertise(h store.Host, tunnelAddr string, macvtap bool) string {
	if h.ParentID == 0 && tunnelAddr != h.Addr {
		return tunnelAddr
	}
	if macvtap && h.Addr != "" {
		return h.Addr
	}
	return ""
}

func NewClusterRunner(s *Server) *ClusterRunner { return &ClusterRunner{m: s.hub, s: s} }

func (r *ClusterRunner) Resumable() bool { return true }

// Run создаёт кластер (или добавляет worker'ы — те же шаги для новых
// машин).
func (r *ClusterRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ClusterJobParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	if p.DryRun && p.Spec != nil {
		jc.StepKey(1, 1, "hub.clusterStepPreflight")
		failed, err := r.runPreflight(ctx, jc, *p.Spec, p.Prepare, 0)
		if err != nil {
			return err
		}
		if len(failed) > 0 {
			return preflightError(failed)
		}
		return nil
	}
	cl, err := r.m.db.ClusterByID(ctx, p.ClusterID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return msgs.Errorf("hub.clusterGone")
		}
		return err
	}
	var spec ClusterSpec
	if err := json.Unmarshal([]byte(cl.SpecJSON), &spec); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	var done clusterResume
	if err := jc.LoadResume(&done); err != nil {
		return msgs.Errorf("hub.parsingResumeState", err)
	}
	if cl.Status == store.ClusterFailed {
		// Продолжение после провала: кластер снова «создаётся».
		_ = r.m.db.SetClusterStatus(ctx, cl.ID, store.ClusterCreating, "")
	}
	if done.Hosts == nil {
		done.Hosts = map[string]int64{}
	}
	if done.Installed == nil {
		done.Installed = map[string]bool{}
	}
	err = r.run(ctx, jc, cl, spec, p, &done)
	if err != nil {
		_ = r.m.db.SetClusterStatus(context.Background(), cl.ID, store.ClusterFailed, msgs.Localize(jc.Lang(), err))
		return err
	}
	return r.m.db.SetClusterStatus(context.Background(), cl.ID, store.ClusterReady, "")
}

func (r *ClusterRunner) run(ctx context.Context, jc *jobs.Context, cl store.Cluster, spec ClusterSpec, p ClusterJobParams, done *clusterResume) error {
	nodes, cp1Name, err := clusterResolveNodes(ctx, r.m.db, cl, spec, p)
	if err != nil {
		return msgs.Errorf("hub.clusterNoNodes")
	}
	if p.AddWorkers > 0 {
		// Пополнение: существующие узлы уже стоят — отметим их, чтобы шаги
		// не пересоздавали их, а новые worker'ы прошли конвейер.
		existing, _ := r.m.db.ClusterHosts(ctx, cl.ID)
		for _, h := range existing {
			done.Hosts[h.Name] = h.ID
			done.Installed[h.Name] = true
		}
	}
	var cps []clusterNode
	for _, nd := range nodes {
		if nd.Role == RoleControlPlane {
			cps = append(cps, nd)
		}
	}
	// Гейт: какие шаги исполнять и какие принудительно. При явном выборе
	// оператора (окно шагов) бежим ровно по отмеченным; иначе — как раньше,
	// пропуск решает Resume.
	gate := r.clusterBuildGate(ctx, spec, nodes, cp1Name, p, done)
	total := len(nodes) + 4
	if spec.NetworkMode == NetworkWireGuard {
		total++
	}
	if spec.CNI == "cilium" {
		total++
	}
	if spec.Endpoint != "" {
		total++
	}
	if spec.LBEnabled {
		total++
	}
	if len(spec.Addons) > 0 {
		total++
	}
	step := 0

	// 0. Проверки — до первой машины. При продолжении после перезапуска
	// не повторяются: машины уже есть, и «имя занято» было бы ложью.
	step++
	jc.StepKey(step, total, "hub.clusterStepPreflight")
	if gate.run[stepPreflight] {
		failed, err := r.runPreflight(ctx, jc, spec, false, cl.ID)
		if err != nil {
			return err
		}
		if len(failed) > 0 {
			// Ничего ещё не создано — запись не нужна: иначе в списке
			// висит «кластер» без единой машины, а следующая попытка
			// спотыкается о занятое имя.
			_ = r.m.db.DeleteCluster(context.Background(), cl.ID)
			jc.Log("hub.clusterDiscarded", spec.Name)
			return preflightError(failed)
		}
	} else {
		jc.Log("hub.preflightSkipped")
	}

	// 0.5. Туннель между хостами: машины на каждом хосте — в своей сети
	// libvirt, подсети соседей — через WireGuard.
	var plan *wgPlan
	if spec.NetworkMode == NetworkWireGuard {
		step++
		jc.StepKey(step, total, "hub.clusterStepMesh")
		var err error
		if gate.run[stepMesh] && !done.MeshUp {
			if plan, err = r.setupMesh(ctx, jc, cl, spec, nodes); err != nil {
				return err
			}
			done.MeshUp = true
			jc.SaveResume(*done)
		} else {
			if plan, err = r.m.clusterWG(cl); err != nil {
				return err
			}
			if plan == nil {
				return msgs.Errorf("hub.clusterMeshMissing")
			}
			jc.Log("hub.clusterMeshExists", plan.Iface)
		}
		for i := range nodes {
			if nodes[i].Kind == KindVM {
				nodes[i].Network, nodes[i].Bridge = plan.Iface, ""
			}
		}
	}
	// nodeAddr — адрес узла для остальных: у узла-хоста в туннеле — его
	// адрес в туннеле, иначе обычный.
	nodeAddr := func(h store.Host) string {
		if plan != nil && h.ParentID == 0 {
			if wh := plan.host(h.ID); wh != nil {
				return wh.IP
			}
		}
		return h.Addr
	}
	// macvtapOf — узлы, включённые в устройство хоста: у машины вне сети
	// libvirt адрес тот, что сообщил гостевой агент, и kubelet обязан
	// взять именно его, а не первый найденный на интерфейсе.
	macvtapOf := map[string]bool{}
	for _, nd := range nodes {
		if nd.Nic != "" {
			macvtapOf[nd.Name] = true
		}
	}

	// 1. Узлы: машины — по одной, сам хост — просто запись.
	for _, nd := range nodes {
		step++
		jc.StepKey(step, total, "hub.clusterStepVM", nd.Name)
		if !gate.run[vmStepID(nd.Name)] {
			// Шаг не выбран (или машина уже заведена в прежнем режиме) —
			// не пересоздаём. Скажем, что машина есть, если запись жива.
			if id := done.Hosts[nd.Name]; id != 0 {
				if _, err := r.m.db.HostByID(ctx, id); err == nil {
					jc.Log("hub.clusterVMExists", nd.Name)
				}
			}
			continue
		}
		if nd.Kind == KindHost {
			h, err := r.m.db.HostByID(ctx, nd.HostID)
			if err != nil {
				return err
			}
			_ = r.m.db.SetHostCluster(ctx, h.ID, cl.ID, nd.Role)
			done.Hosts[nd.Name] = h.ID
			jc.SaveResume(*done)
			jc.Log("hub.clusterHostNode", h.Name, nd.Role)
			continue
		}
		if err := r.createNode(ctx, jc, spec, cl.ID, nd, done); err != nil {
			return err
		}
	}

	// 1.5. Слушатель общего адреса на шлюзе Traffic Fly — до первого
	// control plane: kubeadm в конце init проверяет API именно по
	// control-plane-endpoint (фаза apicheck), и без живого адреса init не
	// проходит, сколько бы API ни слушал локально.
	// Слушатель нужен — ждать ли адрес при входах; не нужен (общий адрес
	// обслуживает кто-то другой) — узлы войдут сами, как умели и раньше.
	listener := false
	if spec.Endpoint != "" {
		step++
		jc.StepKey(step, total, "hub.clusterStepListener")
		if gate.run[stepListener] {
			var err error
			listener, err = r.clusterListenerStep(ctx, jc, cl, spec)
			if err != nil {
				return err
			}
			if listener {
				done.Listener = true
				jc.SaveResume(*done)
			}
		} else {
			// Шаг не выбран: слушатель уже разложен ранее. Живость адреса
			// всё равно проверяется ниже (гейты перед входами и дополнениями).
			listener = true
			jc.Log("hub.clusterListenerExists")
		}
	}

	// 1.6. Балансировщик — установка HAProxy (+ keepalived при HA-паре) на
	// машинах lb-*. Зависит от vm:cp-* (адреса cp нужны для HAProxy-конфига).
	// Endpoint кластера = VIP (HA) или адрес lb-1 — без живого адреса cp1
	// init не пройдёт (apicheck).
	if spec.LBEnabled {
		step++
		jc.StepKey(step, total, "hub.clusterStepLB")
		if gate.run[stepLB] {
			if err := r.installLB(ctx, jc, spec, nodes, done); err != nil {
				return err
			}
			done.LB = true
			jc.SaveResume(*done)
			// Endpoint кластера теперь известен: VIP или адрес lb-1.
			// Записываем в spec, чтобы cp1 использовал его.
			if spec.Endpoint == "" {
				spec.Endpoint = r.lbEndpoint(spec, done)
			}
		} else {
			jc.Log("hub.clusterLBExists")
		}
	}

	// 2. Control plane и токен.
	step++
	jc.StepKey(step, total, "hub.clusterStepControlPlane")
	if spec.Flavor == k8s.FlavorKubeadm && spec.K8sVersion == "" {
		// Ветка pkgs.k8s.io — одна на весь кластер, записывается в spec.
		spec.K8sVersion = r.m.k8sStableMinor(ctx)
		if raw, err := json.Marshal(spec); err == nil {
			_ = r.m.db.SetClusterSpec(ctx, cl.ID, string(raw))
		}
	}
	if spec.K8sVersion != "" {
		jc.Log("hub.clusterK8sVersion", spec.K8sVersion)
	} else {
		jc.Log("hub.clusterK3sStable")
	}
	cp1, err := r.m.db.HostByID(ctx, done.Hosts[cp1Name])
	if err != nil {
		return err
	}
	cpHost, err := r.m.db.HostByID(ctx, cl.HostID)
	if err != nil {
		return err
	}
	if cp1.ParentID == 0 {
		// Узел — сам хост: проброс не нужен, адрес API — его собственный.
		cpHost = cp1
	}
	cp1Addr := nodeAddr(cp1)
	tlsSANs := []string{cp1Addr}
	if cp1.Addr != cp1Addr {
		tlsSANs = append(tlsSANs, cp1.Addr)
	}
	if spec.Expose && cpHost.Addr != "" && cpHost.ID != cp1.ID {
		tlsSANs = append(tlsSANs, cpHost.Addr)
	}
	if gate.run[stepCP1] {
		is := k8s.InstallSpec{
			Flavor: spec.Flavor, Role: k8s.RoleServer, Single: len(nodes) == 1 && p.AddWorkers == 0,
			ClusterInit: len(cps) == 3, TLSSANs: tlsSANs, NodeName: cp1Name,
			CNI: spec.CNI, KubeProxyReplacement: spec.KubeProxyReplacement, CiliumValues: spec.CiliumValues,
			APIAddr: cp1Addr, Version: spec.K8sVersion, ControlPlaneEndpoint: spec.Endpoint,
		}
		is.NodeIP = nodeIPAdvertise(cp1, cp1Addr, macvtapOf[cp1Name])
		is = gate.forceInstallSpec(stepCP1, is)
		if gate.force[stepCP1] {
			jc.Log("hub.clusterForceReinstall", cp1Name)
		}
		if err := r.installRole(ctx, jc, cp1, is); err != nil {
			return err
		}
		done.Installed[cp1Name] = true
		jc.SaveResume(*done)
	}
	// Узлы входят по общему адресу, когда он задан: у kubeadm это тот же
	// --control-plane-endpoint, что у init, и сертификат API каждого
	// control plane содержит его имя уже. control_plane=1 просит ключ
	// сертификатов — он нужен, если в этом задании встанет ещё один
	// control plane; ключ живёт два часа, поэтому берётся перед входом,
	// а не один раз при создании. Токен берётся только когда в этом прогоне
	// действительно есть узлы для входа: иначе (например, повтор только
	// дополнений) лишний поход к cp-1 мог бы уронить задание впустую.
	needJoin := false
	for _, nd := range nodes {
		if nd.Name != cp1Name && gate.run[joinStepID(nd.Name)] {
			needJoin = true
			break
		}
	}
	// Узел входит по общему адресу, значит, адрес уже должен отвечать:
	// первый control plane по сводке шлюза жив — можно входить. Проверку
	// делает сам шлюз, со своего адреса, поэтому она не видит белого
	// списка: «жива» означает «путь есть», а не «вход разрешён». Именно
	// поэтому отказ разбирается по строкам журнала, а не по слову «не
	// отвечает». Ждём только когда в этом прогоне кто-то входит или ставится
	// cp-1: у повтора одних дополнений своя проверка живости ниже.
	if listener && (gate.run[stepCP1] || needJoin) &&
		r.clusterListenerAlive(ctx, jc, clusterListenerID(spec.Name), []string{cp1Name}) == 0 {
		return msgs.Errorf("hub.clusterListenerDead", spec.Endpoint, clusterListenerID(spec.Name))
	}
	var join k8s.JoinInfo
	if needJoin {
		joinAddr := cp1Addr
		if spec.Endpoint != "" {
			joinAddr = spec.Endpoint
		}
		joinURL := "/api/k8s/join?server=" + url.QueryEscape(joinAddr)
		if len(cps) > 1 {
			joinURL += "&control_plane=1"
		}
		// Сразу после init API за общим адресом устанавливается не мгновенно:
		// бэкенды балансировщика ещё DOWN, kubeadm внутри агента крутит свои
		// повторы, и одна попытка хаба ловила это окно вплоть до таймаута
		// клиента. Три попытки с паузой окно переживают; старый агент
		// (404/405) не повторяется — там повтор ничего не исправит.
		var err error
		for attempt := 1; ; attempt++ {
			code, callErr := r.m.HostAPI(ctx, cp1.ID, "GET", joinURL, nil, &join)
			if callErr == nil {
				err = nil
				break
			}
			err = callErr
			if attempt >= 3 || code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
				break
			}
			jc.Log("hub.clusterJoinRetry", attempt, msgs.Localize(jc.Lang(), callErr))
			if !sleepCtx(ctx, 10*time.Second) {
				return ctx.Err()
			}
		}
		if err != nil {
			return msgs.Errorf("hub.clusterJoinInfo", err)
		}
		jc.Log("hub.clusterJoinReady", cp1.Name)
	}

	// 3. Остальные узлы: сначала control plane, потом worker'ы (nodes уже
	// в этом порядке).
	step++
	jc.StepKey(step, total, "hub.clusterStepJoin")
	for _, nd := range nodes {
		id := joinStepID(nd.Name)
		// Машины балансировщика в кластер не входят — они не узлы Kubernetes.
		if nd.Name == cp1Name || nd.Role == RoleLoadBalancer || !gate.run[id] {
			continue
		}
		h, err := r.m.db.HostByID(ctx, done.Hosts[nd.Name])
		if err != nil {
			return err
		}
		is := k8s.InstallSpec{Flavor: spec.Flavor, Role: k8s.RoleAgent, ServerURL: join.ServerURL, Token: join.Token, CAHash: join.CAHash, NodeName: nd.Name,
			CNI: spec.CNI, KubeProxyReplacement: spec.KubeProxyReplacement, APIAddr: cp1Addr, Version: spec.K8sVersion}
		is.NodeIP = nodeIPAdvertise(h, nodeAddr(h), nd.Nic != "")
		if nd.Role == RoleControlPlane {
			is.Role, is.CertKey, is.TLSSANs = k8s.RoleServer, join.CertKey, tlsSANs
		}
		is = gate.forceInstallSpec(id, is)
		if gate.force[id] {
			jc.Log("hub.clusterForceReinstall", nd.Name)
		}
		if err := r.installRole(ctx, jc, h, is); err != nil {
			return err
		}
		done.Installed[nd.Name] = true
		jc.SaveResume(*done)
	}

	// 4. Cilium — отдельный шаг: все узлы уже связаны (зарегистрированы в
	// API, пусть и NotReady без CNI), теперь ставится CNI. Идёт ДО ожидания
	// Ready: без CNI узел легально NotReady, и ждать Ready раньше CNI
	// бессмысленно. Ставится только если выбран в форме (spec.CNI == "cilium").
	if spec.CNI == "cilium" {
		step++
		jc.StepKey(step, total, "hub.clusterStepCilium")
		if gate.run[stepCilium] {
			// Установка на cp1: install или upgrade в зависимости от
			// существования релиза Helm. Values — из spec.CiliumValues.
			// Адрес API — как у cp1: при замене kube-proxy агенту нужен
			// прямой адрес, а не ClusterIP.
			is := k8s.InstallSpec{
				Flavor: spec.Flavor, Role: k8s.RoleServer,
				CNI: spec.CNI, KubeProxyReplacement: spec.KubeProxyReplacement,
				CiliumValues: spec.CiliumValues,
				APIAddr:      cp1Addr, Version: spec.K8sVersion,
				ControlPlaneEndpoint: spec.Endpoint,
			}
			if err := r.installCilium(ctx, jc, cp1, is); err != nil {
				return err
			}
			done.Cilium = true
			jc.SaveResume(*done)
		} else {
			jc.Log("hub.clusterCiliumSkipped")
		}
	}

	// 5. Ready, проброс, kubeconfig. Адрес API считается в любом случае —
	// он нужен финальной строке журнала и не зависит от выбора шагов.
	step++
	serverAddr := spec.apiAddr(cpHost.Addr, cp1.Addr)
	if cp1.ParentID == 0 && spec.Endpoint == "" {
		// Узел-хост: kubeconfig — на его обычный адрес, он в SAN.
		serverAddr = cp1.Addr + ":6443"
	}
	jc.StepKey(step, total, "hub.clusterStepReady")
	if gate.run[stepReady] {
		// Ждём только узлы Kubernetes: машины балансировщика в кластер не
		// входят и в списке узлов API не появятся никогда.
		want := 0
		for _, nd := range nodes {
			if nd.Role != RoleLoadBalancer && done.Hosts[nd.Name] != 0 {
				want++
			}
		}
		if err := r.waitNodesReady(ctx, jc, cp1, want); err != nil {
			return err
		}
		if spec.Expose && !done.Exposed && cp1.ParentID != 0 {
			if _, err := r.m.HostAPI(ctx, cpHost.ID, "POST", "/api/vm/portforward",
				map[string]any{"name": cp1.Name, "ip": cp1.Addr, "rules": spec.exposeRules()}, nil); err != nil {
				return msgs.Errorf("hub.clusterExpose", err)
			}
			done.Exposed = true
			jc.SaveResume(*done)
			jc.Log("hub.clusterExposed", cpHost.Addr, cp1.Addr, exposeSummary(spec))
		}
		var kubeconfig string
		code, err := r.m.HostAPI(ctx, cp1.ID, "GET", "/api/k8s/kubeconfig?server="+url.QueryEscape(serverAddr), nil, &kubeconfig)
		if err != nil || code != 200 {
			return msgs.Errorf("hub.clusterKubeconfig", err, code)
		}
		enc, err := secretbox.Encrypt(r.m.key, []byte(kubeconfig))
		if err != nil {
			return err
		}
		if err := r.m.db.SetClusterKubeconfig(ctx, cl.ID, serverAddr, enc); err != nil {
			return err
		}
		if p.AddWorkers > 0 {
			_ = r.m.db.SetClusterWorkers(ctx, cl.ID, cl.Workers+p.AddWorkers)
		}
	} else {
		jc.Log("hub.clusterReadySkipped")
	}

	// 6. Дополнения: подготовить узлы этого запуска (при пополнении —
	// только новые worker'ы: чарт уже стоит, его DaemonSet придёт на них
	// сам) и поставить чарт на cp1.
	if len(spec.Addons) > 0 {
		step++
		jc.StepKey(step, total, "hub.clusterStepAddons")
		if !gate.run[stepAddons] {
			jc.Log("hub.clusterAddonsSkipped")
		} else {
			// Дополнения входят в кластер с узла и по общему адресу (kubeconfig
			// и --apiserver-advertise-address указывают на него): к этому шагу
			// адрес обязан отвечать не только первому control plane. Ждём все
			// цели применённого слушателя и называем то, что не служит, — иначе
			// Cilium падал бы с SSL_ERROR_SYSCALL, ничего не объясняя.
			if listener {
				route := clusterListenerID(spec.Name)
				if r.clusterListenerAlive(ctx, jc, route, r.clusterListenerTargets(ctx, route)) == 0 {
					return msgs.Errorf("hub.clusterListenerDead", spec.Endpoint, route)
				}
			}
			var prepare []store.Host
			k8sNodes := addonPrepareNodes(nodes)
			for _, nd := range k8sNodes {
				if h, err := r.m.db.HostByID(ctx, done.Hosts[nd.Name]); err == nil {
					prepare = append(prepare, h)
				}
			}
			if err := r.m.applyAddons(ctx, jc, spec, cp1, prepare, len(k8sNodes), p.AddWorkers == 0, done); err != nil {
				return err
			}
		}
	}
	// 6. Автомат целей Traffic Fly: маршруты, ведущие в этот кластер,
	// перекладываются на его узлы. Неудача — только в журнал: кластер
	// собран, а карта Traffic Fly не должна рвать задание создания из-за
	// недоступного шлюза (минутное наблюдение повторит само).
	if err := r.s.flyClusterReconcile(ctx, jc, jc.Job.Author, spec.Name); err != nil {
		jc.Log("hub.clusterFlyWarn", spec.Name, err)
	}
	jc.Log("hub.clusterDone", spec.Name, serverAddr)
	return nil
}

// K8sVersions — актуальная минорная версия и три предыдущие (столько
// веток поддерживает Kubernetes; у k3s каналы на них тоже есть).
func (m *Manager) K8sVersions(ctx context.Context) (stable string, versions []string) {
	stable = m.k8sStableMinor(ctx)
	parts := strings.Split(stable, ".")
	major, minor := parts[0], 0
	fmt.Sscanf(parts[1], "%d", &minor)
	for i := 0; i < 4 && minor-i >= 0; i++ {
		versions = append(versions, fmt.Sprintf("%s.%d", major, minor-i))
	}
	return stable, versions
}

// k8sStableMinor — актуальная минорная версия Kubernetes по
// dl.k8s.io/release/stable.txt («v1.36.2» → «1.36»); недоступно —
// k8s.DefaultKubeadmVersion. Кэшируется на час.
func (m *Manager) k8sStableMinor(ctx context.Context) string {
	m.k8sStableMu.Lock()
	defer m.k8sStableMu.Unlock()
	if m.k8sStable != "" && time.Since(m.k8sStableAt) < time.Hour {
		return m.k8sStable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://dl.k8s.io/release/stable.txt", nil)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		defer resp.Body.Close()
		if raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64)); resp.StatusCode == 200 {
			v := strings.TrimPrefix(strings.TrimSpace(string(raw)), "v")
			if parts := strings.Split(v, "."); len(parts) >= 2 && k8sMinorRe.MatchString(parts[0]+"."+parts[1]) {
				m.k8sStable, m.k8sStableAt = parts[0]+"."+parts[1], time.Now()
				return m.k8sStable
			}
		}
	}
	return k8s.DefaultKubeadmVersion
}

// exposeSummary — «16443→6443, 80→80» для журнала.
func exposeSummary(s ClusterSpec) string {
	var parts []string
	for _, r := range s.exposeRules() {
		parts = append(parts, fmt.Sprintf("%d→%d", r["host_port"], r["vm_port"]))
	}
	return strings.Join(parts, ", ")
}

// createNode создаёт машину заданием VMProvision и ждёт его.
func (r *ClusterRunner) createNode(ctx context.Context, jc *jobs.Context, spec ClusterSpec, clusterID int64, nd clusterNode, done *clusterResume) error {
	host, err := r.m.db.HostByID(ctx, nd.HostID)
	if err != nil {
		return err
	}
	imageID := nd.ImageID
	if name := hubImageName(imageID); name != "" {
		// Свой образ из библиотеки хаба: на хост — если его там ещё нет.
		img, err := r.m.ClusterImage(name)
		if err != nil {
			return err
		}
		if err := r.m.ensureHostImage(ctx, jc, host.ID, host.Name, img); err != nil {
			return err
		}
		imageID = vmcreate.HostPrefix + name
	}
	vm := vmcreate.Spec{Name: nd.Name, ImageID: imageID, Network: nd.Network, Bridge: nd.Bridge, Direct: nd.Nic,
		User: spec.User, Autostart: true, VCPUs: nd.VCPUs, MemoryMB: nd.MemMB, DiskGB: nd.DiskGB}
	if nd.Bridge != "" || nd.Nic != "" {
		// Вне сети libvirt адрес машины узнаётся через гостевого агента:
		// пакет ставит cloud-init, а службу поднимает скрипт первого
		// запуска (vmcreate.baseUserData) — сам пакет адреса хаб бы не дал.
		// Без агента хаб не узнал, куда ходить к узлу.
		vm.Packages = append(vm.Packages, "qemu-guest-agent")
	}
	// Пакеты дополнений (Longhorn — open-iscsi и nfs-common) едут сюда же,
	// в первый запуск машины: шагом подготовки apt попал бы уже на узел с
	// Cilium вместо kube-proxy, где исходящий TCP в NAT-сети хоста может не
	// работать.
	// Load-balancer machines never run Longhorn, so they get no addon
	// packages: the trimmed LB image does not need iSCSI or NFS client.
	if nd.Role != RoleLoadBalancer {
		for _, a := range spec.Addons {
			vm.Packages = append(vm.Packages, k8s.AddonPackages(a.Name)...)
		}
	}
	if r.s.jobs == nil {
		return msgs.Errorf("api.backgroundJobsAreUnavailable")
	}
	jobID, err := r.s.jobs.Start(ctx, jobs.Spec{
		Kind: KindVMProvision, TitleKey: "hub.machineOnHostJobTitle", TitleArgs: []any{nd.Name, host.Name},
		Queue: fmt.Sprintf("vm:%d", host.ID), Author: jc.Job.Author, Steps: 5,
		Params: VMProvisionParams{HostID: host.ID, Spec: vm, InstallNKT: true},
	})
	if err != nil {
		return err
	}
	sr := &ScriptRunner{s: r.s, m: r.m}
	if err := sr.waitHubJob(ctx, jc, jobID); err != nil {
		return err
	}
	hosts, err := r.m.db.ListHosts(ctx)
	if err != nil {
		return err
	}
	for _, h := range hosts {
		if h.Name == nd.Name && h.ParentID == host.ID {
			_ = r.m.db.SetHostCluster(ctx, h.ID, clusterID, nd.Role)
			done.Hosts[nd.Name] = h.ID
			jc.SaveResume(*done)
			// NAT-сеть хоста: адрес машины виден из неё только через аренду
			// DHCP или ARP, а обе вещи не вечные: аренда появляется не в
			// первый запрос и истекает, запись ARP стареет за минуты. Хаб
			// ходит к узлу по SSH и открывает на него маршрут Traffic Fly —
			// и то, и другое отказывает на ровном месте («адреса цели нет»),
			// пока машина спокойно работает. Бронь по MAC закрепляет за узлом
			// тот адрес, который он уже взял, и снимок хоста отдаёт его
			// всегда. Узлу на мосту закреплять нечего: сети libvirt с её
			// DHCP там нет, адрес машины сообщает гостевой агент. В
			// туннельном режиме узлы видят друг друга по адресам плана, но
			// машина на каждом хосте живёт в своей NAT-сети, и хаб этот
			// адрес у хоста всё равно спрашивает.
			if nd.Nic != "" {
				// macvtap: сети libvirt с её DHCP-арендой тут нет, адрес
				// сообщает гостевой агент, и закрепить его хаб нечем — это
				// делает администратор сети (бронь за MAC или статика на
				// узле). Без закрепления адрес уезжает при следующей
				// выдаче DHCP, и узел выпадает из кластера с «no route to
				// host» к старому.
				jc.Log("hub.clusterAddressStatic", nd.Name, nd.Nic)
			} else if nd.Bridge == "" {
				var fixed struct {
					Address string `json:"address"`
				}
				if _, err := r.m.HostAPI(ctx, host.ID, "POST", vmPath(h, "/fixed-address"), nil, &fixed); err != nil {
					jc.Log("hub.clusterFixAddressWarn", nd.Name, err)
				} else {
					jc.Log("hub.clusterFixAddress", nd.Name, fixed.Address)
				}
			}
			return nil
		}
	}
	return msgs.Errorf("hub.clusterVMMissing", nd.Name)
}

// installRole ставит роль заданием хоста и ждёт его.
func (r *ClusterRunner) installRole(ctx context.Context, jc *jobs.Context, h store.Host, spec k8s.InstallSpec) error {
	// Шаги установки выполняет nkt на самом узле — своей версии. Узел,
	// созданный старым хабом, или «железный» хост с отставшим nkt получат
	// старые скрипты; перед ролью узел обновляется до версии хаба.
	if cur, err := r.m.db.HostByID(ctx, h.ID); err == nil {
		h = cur
	}
	if h.NktVersion != "" && h.NktVersion != r.m.Version() {
		jc.Log("hub.clusterNodeUpdate", h.Name, h.NktVersion, r.m.Version())
		vr := &VMProvisionRunner{m: r.m}
		if err := vr.runInstall(ctx, jc, h.ID); err != nil {
			return msgs.Errorf("hub.clusterNodeUpdateFailed", h.Name, err)
		}
	}
	jc.Log("hub.clusterInstalling", spec.Flavor, spec.Role, h.Name)
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/k8s/install", spec, &started); err != nil {
		return msgs.Errorf("hub.clusterInstallStart", h.Name, err)
	}
	g := &GroupApplyRunner{m: r.m}
	return g.waitHostJob(ctx, jc, h, started.JobID)
}

// installLB ставит балансировщик (HAProxy + keepalived) на машинах lb-*.
// Скрипт генерирует конфиги по адресам cp-узлов и параметрам HAProxy,
// устанавливает пакеты, запускает службы.
func (r *ClusterRunner) installLB(ctx context.Context, jc *jobs.Context, spec ClusterSpec, nodes []clusterNode, done *clusterResume) error {
	// Собираем адреса cp-узлов для HAProxy-конфига.
	var cpAddrs []string
	for _, nd := range nodes {
		if nd.Role == RoleControlPlane {
			if h, err := r.m.db.HostByID(ctx, done.Hosts[nd.Name]); err == nil {
				cpAddrs = append(cpAddrs, h.Addr+":6443")
			}
		}
	}
	if len(cpAddrs) == 0 {
		return msgs.Errorf("hub.clusterLBNoCP")
	}

	// Определяем адрес endpoint'а: VIP (HA) или адрес lb-1.
	endpoint := r.lbEndpoint(spec, done)
	if endpoint == "" {
		return msgs.Errorf("hub.clusterLBNoEndpoint")
	}

	// Параметры HAProxy со значениями по умолчанию.
	haproxy := spec.LBHaproxy
	if haproxy.ConnectTimeout == 0 {
		haproxy.ConnectTimeout = 5000
	}
	if haproxy.ClientTimeout == 0 {
		haproxy.ClientTimeout = 50000
	}
	if haproxy.ServerTimeout == 0 {
		haproxy.ServerTimeout = 50000
	}
	if haproxy.MaxConn == 0 {
		haproxy.MaxConn = 2000
	}
	if haproxy.Algorithm == "" {
		haproxy.Algorithm = "source-hash"
	}

	// Машины HA-пары: каждой нужен адрес соседки — keepalived шлёт ей
	// анонсы unicast'ом (мультикаст в macvtap на одном хосте не ходит, и
	// пара держала бы VIP вдвоём).
	var lbs []store.Host
	for _, nd := range nodes {
		if nd.Role != RoleLoadBalancer {
			continue
		}
		h, err := r.m.db.HostByID(ctx, done.Hosts[nd.Name])
		if err != nil {
			return err
		}
		lbs = append(lbs, h)
	}

	// Собираем параметры для скрипта.
	var params struct {
		Endpoint string          `json:"endpoint"`
		CPAddrs  []string        `json:"cp_addrs"`
		VIP      string          `json:"vip,omitempty"`
		IsMaster bool            `json:"is_master,omitempty"`
		PeerAddr string          `json:"peer_addr,omitempty"`
		Haproxy  LBHaproxyConfig `json:"haproxy"`
	}
	params.Endpoint = endpoint
	params.CPAddrs = cpAddrs
	params.VIP = spec.VIP
	params.Haproxy = haproxy

	// Устанавливаем на каждой lb-машине.
	for i := range lbs {
		jc.Log("hub.clusterLBInstalling", lbs[i].Name)
		// Определяем, master ли эта машина (для keepalived).
		// Первая lb-машина — master, вторая — backup.
		params.IsMaster = (lbs[i].Name == spec.Name+"-lb-1")
		if len(lbs) == 2 {
			params.PeerAddr = lbs[1-i].Addr
		}
		if _, err := r.m.HostAPI(ctx, lbs[i].ID, "POST", "/api/k8s/lb-install", params, nil); err != nil {
			return msgs.Errorf("hub.clusterLBInstallFailed", lbs[i].Name, err)
		}
	}
	jc.Log("hub.clusterLBInstalled", endpoint)
	return nil
}

// installCilium устанавливает Cilium на cp-1 через HostAPI.
// Использует install или upgrade в зависимости от существования релиза Helm.
func (r *ClusterRunner) installCilium(ctx context.Context, jc *jobs.Context, h store.Host, spec k8s.InstallSpec) error {
	jc.Log("hub.clusterCiliumInstalling", h.Name)
	var started struct {
		JobID int64 `json:"job_id"`
	}
	if _, err := r.m.HostAPI(ctx, h.ID, "POST", "/api/k8s/cilium-install", spec, &started); err != nil {
		return msgs.Errorf("hub.clusterCiliumInstallFailed", h.Name, err)
	}
	g := &GroupApplyRunner{m: r.m}
	return g.waitHostJob(ctx, jc, h, started.JobID)
}

// lbEndpoint возвращает адрес API кластера при включённом LB: VIP (HA) или
// адрес lb-1 (один LB). Адрес lb-1 берётся из записи хоста.
func (r *ClusterRunner) lbEndpoint(spec ClusterSpec, done *clusterResume) string {
	if spec.VIP != "" {
		return spec.VIP + ":6443"
	}
	// Один LB — адрес lb-1.
	lb1Name := spec.Name + "-lb-1"
	if h, err := r.m.db.HostByID(context.Background(), done.Hosts[lb1Name]); err == nil {
		return h.Addr + ":6443"
	}
	return ""
}

// waitNodesReady опрашивает control plane, пока все узлы не станут Ready.
func (r *ClusterRunner) waitNodesReady(ctx context.Context, jc *jobs.Context, cp store.Host, want int) error {
	deadline := time.Now().Add(clusterReadyTimeout)
	lastMsg := ""
	for {
		var res struct {
			Nodes []k8s.Node `json:"nodes"`
		}
		if _, err := r.m.HostAPI(ctx, cp.ID, "GET", "/api/k8s", nil, &res); err == nil {
			ready := 0
			for _, n := range res.Nodes {
				if n.Ready {
					ready++
				}
			}
			msg := fmt.Sprintf("%d/%d", ready, want)
			if msg != lastMsg {
				jc.Log("hub.clusterNodesReady", ready, want)
				lastMsg = msg
			}
			if ready >= want && len(res.Nodes) >= want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.clusterReadyTimeout", clusterReadyTimeout)
		}
		if !sleepCtx(ctx, clusterNodePoll) {
			return ctx.Err()
		}
	}
}

// ClusterDeleteRunner удаляет узлы (машины с дисками), слушатель общего
// адреса и запись.
type ClusterDeleteRunner struct {
	m *Manager
	s *Server
}

func NewClusterDeleteRunner(s *Server) *ClusterDeleteRunner {
	return &ClusterDeleteRunner{m: s.hub, s: s}
}

func (r *ClusterDeleteRunner) Run(ctx context.Context, jc *jobs.Context) error {
	var p ClusterJobParams
	if err := jc.Params(&p); err != nil {
		return msgs.Errorf("hub.parsingJob", err)
	}
	cl, err := r.m.db.ClusterByID(ctx, p.ClusterID)
	if err != nil {
		return err
	}
	hosts, err := r.m.db.ClusterHosts(ctx, cl.ID)
	if err != nil {
		return err
	}
	// Машины, заведённые провалившимся созданием, к кластеру привязаться
	// не успели, но по имени и родителю — его: убираются вместе с ним.
	if all, err := r.m.db.ListHosts(ctx); err == nil {
		known := map[int64]bool{}
		for _, h := range hosts {
			known[h.ID] = true
		}
		for _, h := range all {
			if !known[h.ID] && h.ParentID == cl.HostID && (strings.HasPrefix(h.Name, cl.Name+"-cp-") || strings.HasPrefix(h.Name, cl.Name+"-w-")) {
				hosts = append(hosts, h)
			}
		}
	}
	total := len(hosts) + 1
	for i, h := range hosts {
		jc.StepKey(i+1, total, "hub.clusterStepDeleteVM", h.Name)
		if h.ParentID == 0 {
			// Узел-хост: Kubernetes убирается, запись хоста остаётся.
			if err := r.m.runHostJob(ctx, h.ID, "/api/k8s/uninstall", nil, hostLog(jc, h.Name)); err != nil {
				jc.Log("hub.clusterDeleteVMWarn", h.Name, err)
			}
			_ = r.m.db.SetHostCluster(ctx, h.ID, 0, "")
			jc.Log("hub.clusterHostNodeRemoved", h.Name)
			continue
		}
		if h.ParentID != 0 {
			path := "/api/vms/" + url.PathEscape(h.Name) + "?remove_storage=true&force=true"
			if _, err := r.m.HostAPI(ctx, h.ParentID, "DELETE", path, nil, nil); err != nil {
				jc.Log("hub.clusterDeleteVMWarn", h.Name, err)
			}
			if cl.Expose {
				_, _ = r.m.HostAPI(ctx, h.ParentID, "DELETE", "/api/vm/portforward/"+url.PathEscape(h.Name), nil, nil)
			}
		}
		r.m.CloseHost(h.ID)
		if err := r.m.db.DeleteHost(ctx, h.ID); err != nil {
			return err
		}
	}
	jc.StepKey(total, total, "hub.clusterStepDeleteRecord")
	r.removeMesh(ctx, jc, cl)
	var spec ClusterSpec
	parsed := json.Unmarshal([]byte(cl.SpecJSON), &spec) == nil
	// Слушатель общего адреса: маршрут кластера снимается с шлюза и
	// применяется. Неудача — только в журнал: узлы уже убраны, а запись
	// кластера из-за недоступного шлюза переживать их не должна; карта
	// Traffic Fly показывает осиротевший маршрут.
	if parsed && spec.Endpoint != "" {
		note := msgs.T(jc.Lang(), "hub.clusterListenerRemoveNote", cl.Name)
		if err := r.s.clusterListenerRemoveRoute(ctx, jc, jc.Job.Author, clusterListenerID(cl.Name), note); err != nil {
			jc.Log("hub.clusterListenerRemoveWarn", clusterListenerID(cl.Name), err)
		}
	}
	// Маршруты, привязанные к кластеру (ingress клиентов, NodePort):
	// вести больше некуда — убираются с карты. Та же осторожность, что со
	// слушателем: узлы уже убраны, а запись кластера из-за шлюза
	// переживать их не должна.
	if err := r.s.flyClusterDropRoutes(ctx, jc, jc.Job.Author, cl.Name); err != nil {
		jc.Log("hub.clusterFlyWarn", cl.Name, err)
	}
	// Кластер токена: учёт узлов, порт API и запись владельца.
	if parsed && spec.HostID != 0 {
		hostName := ""
		if h, err := r.m.db.HostByID(ctx, spec.HostID); err == nil {
			hostName = h.Name
		}
		releaseClusterToken(context.WithoutCancel(ctx), r.m.db, spec, hostName)
	}
	forgetOwnCluster(context.WithoutCancel(ctx), r.m.db, cl.ID)
	if err := r.m.db.DeleteCluster(ctx, cl.ID); err != nil {
		return err
	}
	jc.Log("hub.clusterDeleted", cl.Name)
	return nil
}

// Kubeconfig расшифровывает сохранённый kubeconfig.
func (m *Manager) ClusterKubeconfig(ctx context.Context, id int64) (string, error) {
	cl, err := m.db.ClusterByID(ctx, id)
	if err != nil {
		return "", err
	}
	if len(cl.KubeconfigEnc) == 0 {
		return "", msgs.Errorf("hub.clusterNoKubeconfig")
	}
	raw, err := secretbox.Decrypt(m.key, cl.KubeconfigEnc)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
