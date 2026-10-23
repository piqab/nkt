package script

import (
	"github.com/piqab/nkt/internal/msgs"
	"strings"
)

// Refs — что известно хабу на момент проверки: по этому сценарий
// сверяется до запуска, чтобы опечатка в имени профиля не всплыла на
// пятом шаге, когда хосты уже заведены.
type Refs struct {
	Hosts     map[string]bool
	Groups    map[string]bool
	Profiles  map[string]bool
	Pipelines map[string]bool
	Clusters  map[string]bool
	// PipelineStacks — стеки, выложенные конвейерами: «хост|стек» → имя
	// конвейера (для предупреждений Warnings).
	PipelineStacks map[string]string
}

// Warnings — замечания, которые не мешают сохранить и выполнить сценарий:
// «docker stack» по стеку, который выложен конвейером. Поднимать его
// лучше командой выкладки — с ключом registry конвейера и пометкой
// «остановлена», убирать — удалением конвейера (иначе выкладка вернёт
// стек). Сценариям от ИИ они уходят как ошибки — модели на исправление.
func Warnings(sc Script, refs Refs) []Issue {
	var out []Issue
	for _, s := range sc.Steps {
		if s.Kind != KindDockerStack || refs.PipelineStacks == nil {
			continue
		}
		project := StackName(s.Args["path"])
		hosts := s.Hosts
		if len(hosts) == 0 {
			hosts = []string{s.Host}
		}
		for _, h := range hosts {
			pl := refs.PipelineStacks[h+"|"+project]
			if pl == "" {
				continue
			}
			key := "script.warnPipelineStack.up"
			switch s.Action {
			case "stop":
				key = "script.warnPipelineStack.stop"
			case "down":
				key = "script.warnPipelineStack.down"
			}
			out = append(out, Issue{Line: s.Line, Err: msgs.Errorf(key, project, h, pl)})
			break
		}
	}
	return out
}

// StackName — имя стека по пути compose-файла: каталог файла
// (/srv/compose/shop/compose.yaml → shop).
func StackName(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return "stack"
}

// Check сверяет ссылки сценария с тем, что есть в хабе и что заводит сам
// сценарий выше по тексту. Порядок важен: «on web1 …» до «host web1 …»
// — ошибка, как и в самом выполнении.
func Check(sc Script, refs Refs) []Issue {
	var issues []Issue
	fail := func(line int, key string, args ...any) {
		issues = append(issues, Issue{Line: line, Err: msgs.Errorf(key, args...)})
	}
	known := func(m map[string]bool, k string) bool { return (m != nil && m[k]) || sc.Placeholder(k) }
	hosts := map[string]bool{}
	groups := map[string]bool{}
	pipelines := map[string]bool{}
	clusters := map[string]bool{}
	for _, s := range sc.Steps {
		switch s.Kind {
		case KindK8sIngress, KindK8sApply, KindK8sWait:
			if !known(refs.Clusters, s.Name) && !clusters[s.Name] {
				fail(s.Line, "script.unknownCluster2", s.Name)
			}
		case KindFlyRoute:
			if c := s.Args["cluster"]; c != "" && !known(refs.Clusters, c) && !clusters[c] {
				fail(s.Line, "script.unknownCluster2", c)
			}
			for _, t := range s.FlyTargets() {
				if !known(refs.Hosts, t.Host) && !hosts[t.Host] {
					fail(s.Line, "script.unknownHost", t.Host)
				}
			}
		case KindPipeline:
			pipelines[s.Name] = true
		case KindDeploy:
			if !known(refs.Pipelines, s.Name) && !pipelines[s.Name] {
				fail(s.Line, "script.unknownPipeline", s.Name)
			}
		case KindMigrate:
			for _, h := range []string{s.Host, s.Args["to"]} {
				if !known(refs.Hosts, h) && !hosts[h] {
					fail(s.Line, "script.unknownHost", h)
				}
			}
		case KindGroup:
			if known(refs.Groups, s.Name) || groups[s.Name] {
				fail(s.Line, "script.groupExists", s.Name)
			}
			groups[s.Name] = true
			if p := s.Args["profile"]; p != "" && !known(refs.Profiles, p) {
				fail(s.Line, "script.unknownProfile", p)
			}
		case KindHost:
			if known(refs.Hosts, s.Name) || hosts[s.Name] {
				fail(s.Line, "script.hostExists", s.Name)
			}
			hosts[s.Name] = true
			if g := s.Args["group"]; g != "" && !known(refs.Groups, g) && !groups[g] {
				fail(s.Line, "script.unknownGroup", g)
			}
		case KindInstall:
			for _, h := range s.Hosts {
				if !known(refs.Hosts, h) && !hosts[h] {
					fail(s.Line, "script.unknownHost", h)
				}
			}
		default:
			if s.Kind == KindK8sCreate {
				clusters[s.Name] = true
			}
			if s.Host != "" && !known(refs.Hosts, s.Host) && !hosts[s.Host] {
				fail(s.Line, "script.unknownHost", s.Host)
			}
			if s.Kind == KindApplyProfile && !known(refs.Profiles, s.Name) {
				fail(s.Line, "script.unknownProfile", s.Name)
			}
			if p := s.Args["profile"]; s.Kind == KindVMCreate && p != "" && !known(refs.Profiles, p) {
				fail(s.Line, "script.unknownProfile", p)
			}
			if s.Kind == KindVMCreate {
				// Машина заводится хостом с таким именем — дальше к ней
				// можно обращаться.
				hosts[s.Name] = true
			}
		}
	}
	return issues
}
