package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/fly"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/script"
	"github.com/piqab/nkt/internal/store"
)

// Команды сценария для готового кластера и маршрутов Traffic Fly:
// «k8s ИМЯ ingress cilium», «k8s ИМЯ apply», «k8s ИМЯ wait deployment»,
// «fly route», «fly apply». Кластер — по имени; действия идут на первый
// control plane теми же вызовами, что раздел «Кластеры» (манифесты,
// терминал kubectl).

// clusterByName — кластер хаба по имени и его первый control plane.
func (r *ScriptRunner) clusterByName(ctx context.Context, name string) (store.Cluster, store.Host, error) {
	list, err := r.m.db.ListClusters(ctx)
	if err != nil {
		return store.Cluster{}, store.Host{}, err
	}
	var found []store.Cluster
	for _, c := range list {
		if c.Name == name {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return store.Cluster{}, store.Host{}, msgs.Errorf("script.unknownCluster2", name)
	case 1:
	default:
		return store.Cluster{}, store.Host{}, msgs.Errorf("script.clusterAmbiguous", name, len(found))
	}
	hosts, err := r.m.db.ClusterHosts(ctx, found[0].ID)
	if err != nil || len(hosts) == 0 {
		return found[0], store.Host{}, msgs.Errorf("hub.clusterNoNodes")
	}
	return found[0], hosts[0], nil
}

// clusterLater — в сухом прогоне кластер создаётся выше по этому же
// сценарию и ещё не существует.
func clusterLater(done *scriptRunResume, name string) bool { return done.DryClusters[name] }

// k8sIngress — «k8s ИМЯ ingress cilium [port N]»: Ingress Cilium в режиме
// host network заданием на control plane.
func (r *ScriptRunner) k8sIngress(ctx context.Context, jc *jobs.Context, done *scriptRunResume, st script.Step, dry bool) error {
	if dry && clusterLater(done, st.Name) {
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	cl, cp, err := r.clusterByName(ctx, st.Name)
	if err != nil {
		return err
	}
	var spec ClusterSpec
	_ = json.Unmarshal([]byte(cl.SpecJSON), &spec)
	if spec.CNI != "cilium" || !spec.KubeProxyReplacement {
		return msgs.Errorf("script.k8sIngressNeedsCilium", cl.Name)
	}
	if dry {
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	port, _ := strconv.Atoi(st.Args["port"])
	jc.Log("hub.scriptK8sOn", cl.Name, cp.Name)
	return r.s.hostJob(ctx, jc, jc.Job.Author, cp.ID, "/api/k8s/cilium/ingress", map[string]int{"port": port})
}

// k8sApply — «k8s ИМЯ apply» с манифестом блоком: kubectl apply на
// control plane, как «Манифесты» раздела «Кластеры»; в сухом прогоне —
// kubectl diff.
func (r *ScriptRunner) k8sApply(ctx context.Context, jc *jobs.Context, done *scriptRunResume, st script.Step, dry bool) error {
	if dry && clusterLater(done, st.Name) {
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	cl, cp, err := r.clusterByName(ctx, st.Name)
	if err != nil {
		return err
	}
	if dry {
		var res struct {
			Diff string `json:"diff"`
		}
		if _, err := r.m.HostAPI(ctx, cp.ID, "POST", "/api/k8s/yaml/diff", map[string]string{"content": st.Block}, &res); err != nil {
			return err
		}
		r.logTail(jc, res.Diff)
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	var res struct {
		Output string `json:"output"`
	}
	body := map[string]string{"content": st.Block, "note": msgs.T(jc.Lang(), "hub.scriptPipelineNote", jc.Job.ID)}
	if _, err := r.m.HostAPI(ctx, cp.ID, "POST", "/api/k8s/apply", body, &res); err != nil {
		return err
	}
	jc.Log("hub.scriptK8sOn", cl.Name, cp.Name)
	for _, l := range strings.Split(strings.TrimSpace(res.Output), "\n") {
		if l != "" {
			jc.Logf("      %s", l)
		}
	}
	return nil
}

// k8sWait — «k8s ИМЯ wait deployment ИМЯ»: все реплики готовы.
func (r *ScriptRunner) k8sWait(ctx context.Context, jc *jobs.Context, done *scriptRunResume, st script.Step, dry bool) error {
	if dry {
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	_, cp, err := r.clusterByName(ctx, st.Name)
	if err != nil {
		return err
	}
	timeout, _ := time.ParseDuration(st.Args["timeout"])
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ns, name := st.Args["namespace"], st.Args["deployment"]
	path := "/api/k8s/resources?kind=deployments&namespace=" + url.QueryEscape(ns)
	deadline := time.Now().Add(timeout)
	last := "—"
	for {
		var res struct {
			Rows []struct {
				Name      string            `json:"name"`
				Namespace string            `json:"namespace"`
				Cols      map[string]string `json:"cols"`
			} `json:"rows"`
		}
		if _, err := r.m.HostAPI(ctx, cp.ID, "GET", path, nil, &res); err == nil {
			for _, row := range res.Rows {
				if row.Name != name || row.Namespace != ns {
					continue
				}
				last = row.Cols["ready"]
				if a, b, ok := strings.Cut(last, "/"); ok && a == b && a != "0" {
					jc.Log("hub.scriptK8sReady", ns, name, last)
					return nil
				}
			}
		} else {
			last = msgs.Localize(jc.Lang(), err)
		}
		if time.Now().After(deadline) {
			return msgs.Errorf("hub.scriptWaitFailed", st.Text, timeout, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// flyRouteFromStep — маршрут из «fly route …» (цели — узлы кластера или
// список); nil без ошибки — в сухом прогоне кластер появится выше.
func (r *ScriptRunner) flyRouteFromStep(ctx context.Context, done *scriptRunResume, st script.Step, dry bool) (*FlyRoute, error) {
	gw, err := r.s.flyPipelineGateway(ctx, st.Args["gateway"])
	if err != nil {
		return nil, err
	}
	route := FlyRoute{ID: st.Name, Gateway: gw.EdgeID, Kind: st.Action, Names: st.List, TLS: st.Args["tls"] == "gateway", DNS: st.Args["dns"] == "plugin"}
	route.Port, _ = strconv.Atoi(st.Args["port"])
	if route.Port == 0 {
		route.Port = 443
		if st.Action == fly.KindHTTP {
			route.Port = 80
		}
	}
	route.Algorithm = fly.AlgoSourceHash
	if len(route.Names) > 0 {
		route.Algorithm = fly.AlgoLeastConn
	}
	route.Check = fly.Check{Kind: fly.CheckTCP, Path: st.Args["path"]}
	if c := st.Args["check"]; c != "" {
		route.Check.Kind = c
	}
	if c := st.Args["cluster"]; c != "" {
		if dry && clusterLater(done, c) {
			return nil, nil
		}
		cl, _, err := r.clusterByName(ctx, c)
		if err != nil {
			return nil, err
		}
		port, _ := strconv.Atoi(st.Args["target_port"])
		hosts, err := r.m.db.ClusterHosts(ctx, cl.ID)
		if err != nil {
			return nil, err
		}
		for _, h := range hosts {
			t := FlyTarget{ID: fmt.Sprintf("n%d", h.ID), HostID: h.ID, Kind: "host", Port: port}
			if h.ParentID != 0 {
				// Узел — машина хоста: в libvirt она называется как в хабе.
				t.HostID, t.Kind, t.Ref = h.ParentID, "vm", h.Name
			}
			route.Targets = append(route.Targets, t)
		}
	}
	for i, ft := range st.FlyTargets() {
		h, err := r.hostByName(ctx, done, ft.Host)
		if err != nil {
			return nil, err
		}
		route.Targets = append(route.Targets, FlyTarget{ID: fmt.Sprintf("t%d", i+1), HostID: h.ID, Kind: ft.Kind, Ref: ft.Ref, Port: ft.Port})
	}
	return &route, nil
}

// flyRoute — «fly route …»: новый маршрут — записать и применить (как
// блок fly: конвейера); есть — свойства из сценария, цели — плавно
// (новые добавить, дождаться, прежние вывести).
func (r *ScriptRunner) flyRoute(ctx context.Context, jc *jobs.Context, done *scriptRunResume, st script.Step, dry bool) error {
	route, err := r.flyRouteFromStep(ctx, done, st, dry)
	if err != nil {
		return err
	}
	if route == nil {
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	user := jc.Job.Author
	note := msgs.T(jc.Lang(), "hub.scriptPipelineNote", jc.Job.ID)
	d := r.s.flyDoc(ctx)
	idx := slices.IndexFunc(d.Routes, func(x FlyRoute) bool { return x.ID == route.ID })
	jc.Log("hub.scriptFlyRoute", route.ID, len(route.Targets), idx < 0)
	if dry {
		next := FlyDoc{Gateways: d.Gateways, Routes: slices.Clone(d.Routes), Alloc: d.Alloc}
		if idx < 0 {
			next.Routes = append(next.Routes, *route)
		} else {
			next.Routes[idx] = *route
		}
		next.normalize()
		if err := r.s.flyValidate(ctx, next); err != nil {
			return err
		}
		jc.Log("hub.scriptDryWould", st.Text)
		return nil
	}
	log := deployFlyLog{jc}
	if idx < 0 {
		saved, err := r.s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: append(d.Routes, *route)}, user, note)
		if err != nil {
			return err
		}
		return r.s.flyApplyRun(ctx, log, user, FlyApplyParams{Revision: saved.Revision})
	}
	cur := d.Routes[idx]
	want := route.Targets
	route.Targets = cur.Targets
	routes := slices.Clone(d.Routes)
	routes[idx] = *route
	if _, err := r.s.flySave(ctx, FlyDoc{Gateways: d.Gateways, Routes: routes}, user, note); err != nil {
		return err
	}
	return r.s.flyReconcileTargets(ctx, jc, user, route.ID, want, true, note)
}

// flyApply — «fly apply»: применить карту Traffic Fly (сухой прогон — в
// сухом прогоне сценария).
func (r *ScriptRunner) flyApply(ctx context.Context, jc *jobs.Context, dry bool) error {
	d := r.s.flyDoc(ctx)
	return r.s.flyApplyRun(ctx, deployFlyLog{jc}, jc.Job.Author, FlyApplyParams{Revision: d.Revision, DryRun: dry})
}
