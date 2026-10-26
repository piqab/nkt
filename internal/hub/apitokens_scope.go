package hub

import (
	"context"

	"github.com/piqab/nkt/internal/deploy"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// Пределы токена для сценариев, кластеров и переносов: токену с пределом
// по хостам — только то, что целиком на его хостах.

// clusterInScope — все узлы кластера (и его хост) доступны токену.
func (s *Server) clusterInScope(ctx context.Context, id int64) bool {
	allow := s.scopeFilter(ctx)
	// Без предела по хостам — любой кластер из списка токена: узлы общего
	// кластера бывают машинами администратора, и это не мешает работать в
	// своём namespace.
	if t, _ := tokenFromContext(ctx); allow == nil || !t.Scoped() {
		return true
	}
	cl, err := s.db.ClusterByID(ctx, id)
	if err != nil || !allow(cl.HostID) {
		return false
	}
	hosts, err := s.db.ClusterHosts(ctx, id)
	if err != nil {
		return false
	}
	for _, h := range hosts {
		if !allow(h.ID) {
			return false
		}
	}
	return true
}

// scriptScopeError — сценарий целиком в пределе токена (нет предела — nil).
// Заводить хосты и группы, менять общую карту Traffic Fly токену с
// пределом нельзя; остальное — только на его хостах и кластерах. Машины,
// которые сценарий создаёт на разрешённом хосте, дальше считаются его.
func (s *Server) scriptScopeError(ctx context.Context, sc script.Script) error {
	allow := s.scopeFilter(ctx)
	if allow == nil {
		return nil
	}
	byName := map[string]int64{}
	if hosts, err := s.db.ListHosts(ctx); err == nil {
		for _, h := range hosts {
			byName[h.Name] = h.ID
		}
	}
	created := map[string]bool{}
	clusters := map[string]bool{}
	pipelines := map[string]bool{}
	host := func(line int, name string) error {
		if name == "" || created[name] {
			return nil
		}
		if id, ok := byName[name]; ok && allow(id) {
			return nil
		}
		return msgs.Errorf("auth.tokenScriptHost", line, name)
	}
	tok, _ := tokenFromContext(ctx)
	// Шагу нужно действие токена «по правам» — то же, что у API.
	need := func(st script.Step, action string) error {
		if tok.Limited() && !tok.Can(action) {
			return msgs.Errorf("auth.tokenScriptAction", st.Line, action)
		}
		return nil
	}
	for _, st := range sc.Steps {
		var err error
		switch st.Kind {
		case script.KindVMCreate, script.KindVMAction:
			if err := need(st, store.TokenActVMManage); err != nil {
				return err
			}
		case script.KindK8sCreate, script.KindK8sDestroy:
			if err := need(st, store.TokenActK8sCluster); err != nil {
				return err
			}
		case script.KindK8sApply:
			if err := need(st, store.TokenActK8sApply); err != nil {
				return err
			}
		}
		switch st.Kind {
		case script.KindHost, script.KindGroup, script.KindFlyRoute, script.KindFlyApply:
			return msgs.Errorf("auth.tokenScriptGlobal", st.Line, st.Text)
		case script.KindPlugin:
			// Плагин — не на хостах токена: только с его действием.
			if t, ok := tokenFromContext(ctx); !ok || !t.Can(pluginActPrefix+st.Name) {
				return msgs.Errorf("auth.tokenScriptGlobal", st.Line, st.Text)
			}
		case script.KindInstall:
			for _, h := range st.Hosts {
				if err = host(st.Line, h); err != nil {
					break
				}
			}
		case script.KindWait:
			if st.Action != "url" {
				err = host(st.Line, st.Host)
			}
		case script.KindMigrate:
			if err = host(st.Line, st.Host); err == nil {
				err = host(st.Line, st.Args["to"])
			}
		case script.KindVMCreate:
			if err = host(st.Line, st.Host); err == nil {
				created[st.Name] = true
			}
		case script.KindK8sCreate:
			err = host(st.Line, st.Host)
			if rows, perr := script.ParsePlacement(st.Args["nodes"]); err == nil && perr == nil && script.IsPlacement(st.Args["nodes"]) {
				for _, row := range rows {
					if err = host(st.Line, row.Host); err != nil {
						break
					}
				}
			}
			if err == nil {
				clusters[st.Name] = true
			}
		case script.KindK8sIngress, script.KindK8sApply, script.KindK8sWait:
			if !clusters[st.Name] && !s.clusterNameInScope(ctx, st.Name) {
				return msgs.Errorf("auth.tokenScriptCluster", st.Line, st.Name)
			}
		case script.KindPipeline:
			spec, perr := deploy.ParseSpec(st.Block)
			if perr != nil || spec.Compose == nil || spec.Compose.Group != "" {
				return msgs.Errorf("auth.tokenScriptGlobal", st.Line, st.Text)
			}
			for _, h := range spec.Compose.Hosts {
				if err = host(st.Line, h); err != nil {
					break
				}
			}
			pipelines[st.Name] = true
		case script.KindDeploy:
			if pipelines[st.Name] {
				continue
			}
			p, ok := s.pipelineNamed(ctx, st.Name)
			if !ok || !s.pipelineInScope(ctx, p) {
				return msgs.Errorf("auth.tokenScriptPipeline", st.Line, st.Name)
			}
		default:
			names := st.Hosts
			if len(names) == 0 {
				names = []string{st.Host}
			}
			for _, h := range names {
				if err = host(st.Line, h); err != nil {
					break
				}
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// clusterNameInScope — кластер с таким именем есть и целиком в пределе.
func (s *Server) clusterNameInScope(ctx context.Context, name string) bool {
	list, err := s.db.ListClusters(ctx)
	if err != nil {
		return false
	}
	for _, c := range list {
		if c.Name == name {
			return s.clusterInScope(ctx, c.ID)
		}
	}
	return false
}

// pipelineNamed — конвейер хаба по имени.
func (s *Server) pipelineNamed(ctx context.Context, name string) (store.Pipeline, bool) {
	list, err := s.db.ListPipelines(ctx)
	if err != nil {
		return store.Pipeline{}, false
	}
	for _, p := range list {
		if p.Name == name {
			return p, true
		}
	}
	return store.Pipeline{}, false
}
