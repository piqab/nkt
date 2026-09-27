package analyze

import (
	"fmt"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/model"
)

// Находки по кластеру Kubernetes (сводка снимается на control plane, см.
// parse.Kubernetes): поды в цикле падений, долгий Pending, узлы NotReady,
// Deployment/StatefulSet без доступных реплик, висящие PVC, истекающий
// сертификат API-сервера, привилегированные поды и NodePort/LoadBalancer
// мимо файрвола.

// k8sStuckAfter — сколько ждать, прежде чем Pending считать зависшим.
const k8sStuckAfter = 15 * time.Minute

// k8sBadReasons — причины ожидания контейнера, которые сами не пройдут.
var k8sBadReasons = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true,
	"CreateContainerConfigError": true, "CreateContainerError": true, "InvalidImageName": true,
}

func k8sObj(ns, name string) string {
	if ns == "" {
		return name
	}
	return ns + "/" + name
}

// k8sAge — сколько прошло с момента created до скана; ноль, если неизвестно.
func k8sAge(s *model.Snapshot, created string) time.Duration {
	c, err1 := time.Parse(time.RFC3339, created)
	now, err2 := time.Parse(time.RFC3339, s.TS)
	if err1 != nil || err2 != nil {
		return 0
	}
	return now.Sub(c)
}

func ruleKubernetes(c *collector, s *model.Snapshot, idx *index) {
	k := s.K8s
	if k == nil {
		return
	}
	for _, p := range k.Pods {
		obj := k8sObj(p.Namespace, p.Name)
		switch {
		case k8sBadReasons[p.Reason]:
			c.add(model.Finding{
				Rule: "k8s-pod-failing", ID: "k8s-pod-failing:" + obj, Severity: model.SeverityHigh, Service: model.ServiceK8s, Object: obj,
				Title:    fmt.Sprintf("Под %s: %s", obj, p.Reason),
				TitleKey: "finding.k8sPodFailing.title", TitleArgs: []any{obj, p.Reason},
				Detail:    fmt.Sprintf("Контейнер не запускается (%s), перезапусков: %d, узел %s.", p.Reason, p.Restarts, p.Node),
				DetailKey: "finding.k8sPodFailing.detail", DetailArgs: []any{p.Reason, p.Restarts, p.Node},
				Suggestion:    "Откройте журнал пода с галочкой «предыдущий запуск» и «Описание» (Kubernetes → Поды): там причина падения или ошибки загрузки образа.",
				SuggestionKey: "finding.k8sPodFailing.suggestion",
			})
		case p.Phase == "Pending" && k8sAge(s, p.Created) > k8sStuckAfter:
			c.add(model.Finding{
				Rule: "k8s-pod-pending", ID: "k8s-pod-pending:" + obj, Severity: model.SeverityMedium, Service: model.ServiceK8s, Object: obj,
				Title:    fmt.Sprintf("Под %s давно в Pending", obj),
				TitleKey: "finding.k8sPodPending.title", TitleArgs: []any{obj},
				Detail:        "Под не размещён на узле дольше 15 минут: не хватает ресурсов, не подходит ни один узел или не привязан том.",
				DetailKey:     "finding.k8sPodPending.detail",
				Suggestion:    "Посмотрите «Описание» пода — события FailedScheduling называют причину.",
				SuggestionKey: "finding.k8sPodPending.suggestion",
			})
		}
		if len(p.Privileged) > 0 || p.HostNetwork {
			sev := model.SeverityMedium
			if strings.HasPrefix(p.Namespace, "kube-") {
				sev = model.SeverityLow
			}
			what := []string{}
			if len(p.Privileged) > 0 {
				what = append(what, "privileged: "+strings.Join(p.Privileged, ", "))
			}
			if p.HostNetwork {
				what = append(what, "hostNetwork")
			}
			c.add(model.Finding{
				Rule: "k8s-pod-privileged", ID: "k8s-pod-privileged:" + obj, Severity: sev, Service: model.ServiceK8s, Object: obj,
				Title:    fmt.Sprintf("Под %s с правами узла (%s)", obj, strings.Join(what, "; ")),
				TitleKey: "finding.k8sPodPrivileged.title", TitleArgs: []any{obj, strings.Join(what, "; ")},
				Detail:        "Привилегированный контейнер или сеть узла дают поду доступ к хосту: побег из контейнера равен root на узле.",
				DetailKey:     "finding.k8sPodPrivileged.detail",
				Suggestion:    "Уберите privileged и hostNetwork, если они не нужны (агентам мониторинга и CNI — нужны); оставьте такие поды в отдельном namespace.",
				SuggestionKey: "finding.k8sPodPrivileged.suggestion",
			})
		}
	}
	for _, n := range k.Nodes {
		if !n.Ready {
			c.add(model.Finding{
				Rule: "k8s-node-notready", ID: "k8s-node-notready:" + n.Name, Severity: model.SeverityHigh, Service: model.ServiceK8s, Object: n.Name,
				Title:    fmt.Sprintf("Узел кластера %s не готов (NotReady)", n.Name),
				TitleKey: "finding.k8sNodeNotReady.title", TitleArgs: []any{n.Name},
				Detail:    fmt.Sprintf("Kubelet на %s не отчитывается: поды с него будут выселены, новые туда не попадут.", n.Name),
				DetailKey: "finding.k8sNodeNotReady.detail", DetailArgs: []any{n.Name},
				Suggestion:    "Проверьте, включена ли машина и работает ли служба k3s-agent/kubelet (журнал службы на узле).",
				SuggestionKey: "finding.k8sNodeNotReady.suggestion",
			})
		}
	}
	for _, w := range k.Workloads {
		if w.Desired == 0 || w.Available >= w.Desired {
			continue
		}
		obj := k8sObj(w.Namespace, w.Name)
		sev := model.SeverityMedium
		if w.Available == 0 {
			sev = model.SeverityHigh
		}
		c.add(model.Finding{
			Rule: "k8s-workload-unavailable", ID: "k8s-workload-unavailable:" + w.Kind + ":" + obj, Severity: sev, Service: model.ServiceK8s, Object: obj,
			Title:    fmt.Sprintf("%s %s: доступно %d из %d реплик", w.Kind, obj, w.Available, w.Desired),
			TitleKey: "finding.k8sWorkloadUnavailable.title", TitleArgs: []any{w.Kind, obj, w.Available, w.Desired},
			Detail:        "Часть реплик не готова — приложение работает с урезанной ёмкостью или не работает вовсе.",
			DetailKey:     "finding.k8sWorkloadUnavailable.detail",
			Suggestion:    "Посмотрите поды этого объекта: их состояние и журнал обычно показывают причину.",
			SuggestionKey: "finding.k8sWorkloadUnavailable.suggestion",
		})
	}
	for _, v := range k.PVCs {
		if v.Phase != "Pending" || k8sAge(s, v.Created) <= k8sStuckAfter {
			continue
		}
		obj := k8sObj(v.Namespace, v.Name)
		c.add(model.Finding{
			Rule: "k8s-pvc-pending", ID: "k8s-pvc-pending:" + obj, Severity: model.SeverityMedium, Service: model.ServiceK8s, Object: obj,
			Title:    fmt.Sprintf("Том %s не привязан (Pending)", obj),
			TitleKey: "finding.k8sPVCPending.title", TitleArgs: []any{obj},
			Detail:        "Заявка на том ждёт дольше 15 минут: нет подходящего PV или StorageClass не создаёт тома.",
			DetailKey:     "finding.k8sPVCPending.detail",
			Suggestion:    "Проверьте StorageClass заявки и события PVC («Описание»).",
			SuggestionKey: "finding.k8sPVCPending.suggestion",
		})
	}
	now, _ := time.Parse(time.RFC3339, s.TS)
	for _, cert := range k.Certs {
		na, err := time.Parse(time.RFC3339, cert.NotAfter)
		if err != nil || now.IsZero() {
			continue
		}
		left := na.Sub(now)
		if left > 30*24*time.Hour {
			continue
		}
		sev := model.SeverityHigh
		if left <= 0 {
			sev = model.SeverityCritical
		}
		days := int(left.Hours() / 24)
		c.add(model.Finding{
			Rule: "k8s-cert-expiring", ID: "k8s-cert-expiring:" + cert.Path, Severity: sev, Service: model.ServiceK8s, Object: cert.Path, File: cert.Path,
			Title:    fmt.Sprintf("Сертификат API-сервера Kubernetes истекает через %d дн.", days),
			TitleKey: "finding.k8sCertExpiring.title", TitleArgs: []any{days},
			Detail:    fmt.Sprintf("%s действителен до %s. После этого kubectl и узлы перестанут подключаться к кластеру.", cert.Path, cert.NotAfter),
			DetailKey: "finding.k8sCertExpiring.detail", DetailArgs: []any{cert.Path, cert.NotAfter},
			Suggestion:    "kubeadm: kubeadm certs renew all и перезапуск компонентов control plane; k3s обновляет сертификаты сам при перезапуске службы за 90 дней до срока.",
			SuggestionKey: "finding.k8sCertExpiring.suggestion",
		})
	}
	// NodePort и LoadBalancer публикуются правилами kube-proxy в nat
	// PREROUTING — как у Docker, мимо цепочки INPUT, где стоят правила ufw.
	if !s.Firewall.AnyManagerActive() && idx.inputPolicy != "DROP" && idx.inputPolicy != "REJECT" {
		return
	}
	for _, svc := range k.Services {
		if svc.Type != "NodePort" && svc.Type != "LoadBalancer" {
			continue
		}
		for _, p := range svc.Ports {
			ports := []int{p.NodePort}
			if svc.Type == "LoadBalancer" && p.Port != p.NodePort {
				ports = append(ports, p.Port)
			}
			for _, port := range ports {
				if port == 0 || idx.allowedFromAnywhere(port) {
					continue
				}
				obj := fmt.Sprintf("%s:%d", k8sObj(svc.Namespace, svc.Name), port)
				sev := model.SeverityMedium
				if _, ok := sensitivePorts[port]; ok {
					sev = model.SeverityHigh
				}
				c.add(model.Finding{
					Rule: "k8s-port-bypasses-firewall", ID: "k8s-port-bypasses-firewall:" + obj, Severity: sev, Service: model.ServiceK8s, Object: obj,
					Title:    fmt.Sprintf("Сервис %s (%s) открывает порт %d мимо файрвола", k8sObj(svc.Namespace, svc.Name), svc.Type, port),
					TitleKey: "finding.k8sPortBypassesFirewall.title", TitleArgs: []any{k8sObj(svc.Namespace, svc.Name), svc.Type, port},
					Detail:    fmt.Sprintf("kube-proxy публикует порт %d на всех узлах правилами nat PREROUTING: запрет в ufw/firewalld его не закрывает.", port),
					DetailKey: "finding.k8sPortBypassesFirewall.detail", DetailArgs: []any{port},
					Suggestion:    "Если порт не должен быть доступен снаружи — тип ClusterIP и Ingress, либо NetworkPolicy; иначе разрешите его в файрволе явно, чтобы намерение было видно.",
					SuggestionKey: "finding.k8sPortBypassesFirewall.suggestion",
				})
			}
		}
	}
}
