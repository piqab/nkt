package script

import (
	"github.com/piqab/nkt/internal/msgs"
)

// Refs — что известно хабу на момент проверки: по этому сценарий
// сверяется до запуска, чтобы опечатка в имени профиля не всплыла на
// пятом шаге, когда хосты уже заведены.
type Refs struct {
	Hosts    map[string]bool
	Groups   map[string]bool
	Profiles map[string]bool
}

// Check сверяет ссылки сценария с тем, что есть в хабе и что заводит сам
// сценарий выше по тексту. Порядок важен: «on web1 …» до «host web1 …»
// — ошибка, как и в самом выполнении.
func Check(sc Script, refs Refs) []Issue {
	var issues []Issue
	fail := func(line int, key string, args ...any) {
		issues = append(issues, Issue{Line: line, Err: msgs.Errorf(key, args...)})
	}
	known := func(m map[string]bool, k string) bool { return m != nil && m[k] }
	hosts := map[string]bool{}
	groups := map[string]bool{}
	for _, s := range sc.Steps {
		switch s.Kind {
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
