package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/parse"
	"github.com/piqab/nkt/internal/svcdesc"
)

// runServices — `nkt services list` и `nkt services test <файл>`:
// описания сервисов на этом хосте. Ничего не меняет (кроме копии
// последней исправной версии описания в каталоге данных).
func runServices(opts commandOptions) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	c, err := collect.New(string(cfg.Mode), cfg.FixturesRoot, cfg.DockerSocket, cfg.PodmanSocket, cfg.CommandTimeout)
	if err != nil {
		return err
	}
	ctx := context.Background()
	lang := cliLang()
	e := svcdesc.NewEngine(c, cfg.ServicesDir, cfg.DataDir)
	e.Lang = lang
	snap := servicesSnapshot(ctx, c, cfg)
	switch opts.username {
	case "list", "":
		return servicesList(ctx, e, snap, lang)
	case "test":
		if len(opts.args) != 1 {
			return msgs.Errorf("cli.servicesTestUsage")
		}
		return servicesTest(ctx, e, snap, lang, opts.args[0])
	}
	return msgs.Errorf("cli.servicesUsage")
}

// servicesSnapshot — то, что нужно обнаружению: слушающие порты и
// контейнеры.
func servicesSnapshot(ctx context.Context, c collect.Collector, cfg *config.Config) *model.Snapshot {
	snap := &model.Snapshot{}
	snap.Listeners, _ = parse.Listeners(ctx, c)
	d := parse.Docker(ctx, c, cfg.ComposeFiles)
	snap.Container = d.Containers
	snap.Podman = parse.Podman(ctx, c).Containers
	return snap
}

func servicesList(ctx context.Context, e *svcdesc.Engine, snap *model.Snapshot, lang msgs.Lang) error {
	effective, all := e.Load()
	res := e.RunLoaded(ctx, snap, effective)
	engines := e.Engines(ctx, effective, all)
	status := map[string]model.SourceStatus{}
	for _, s := range res.Sources {
		status[s.Name] = s
	}
	counts := map[string][3]int{}
	for _, ep := range res.Endpoints {
		v := counts[ep.Service]
		v[0]++
		counts[ep.Service] = v
	}
	for _, u := range res.Upstreams {
		v := counts[u.Service]
		v[1]++
		counts[u.Service] = v
	}
	for _, f := range res.Findings {
		v := counts[f.Service]
		v[2]++
		counts[f.Service] = v
	}
	active := map[string]string{}
	for _, l := range effective {
		if l.Desc != nil {
			active[l.Desc.Name] = l.File
		}
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, msgs.T(lang, "cli.servicesHeader"))
	for _, l := range all {
		name := l.File
		state := ""
		switch {
		case l.Desc == nil:
			state = msgs.T(lang, "cli.servicesBroken", msgs.Localize(lang, l.Err))
		case l.Desc.Disabled:
			name = l.Desc.Name
			state = msgs.T(lang, "cli.servicesDisabled")
		case active[l.Desc.Name] != l.File:
			name = l.Desc.Name
			state = msgs.T(lang, "cli.servicesOverridden")
		case l.Desc.Engine != "":
			name = l.Desc.Name
			switch c := engines[l.Desc.Engine]; {
			case c.Enabled:
				state = msgs.T(lang, "cli.servicesEngine", optionsText(c.Options))
			default:
				state = msgs.T(lang, "cli.servicesEngineOff")
			}
		default:
			name = l.Desc.Name
			st := status["service:"+name]
			switch {
			case !st.Available:
				state = msgs.T(lang, "cli.servicesNotFound")
			case l.Fallback:
				state = msgs.T(lang, "cli.servicesFallback")
			default:
				state = msgs.T(lang, "cli.servicesFound")
			}
		}
		n := counts[name]
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n", name, l.Origin, state, n[0], n[1], n[2], l.File)
	}
	return w.Flush()
}

func servicesTest(ctx context.Context, e *svcdesc.Engine, snap *model.Snapshot, lang msgs.Lang, file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	d, err := svcdesc.Parse(data)
	if err != nil {
		return err
	}
	e.Trace = func(line string) { fmt.Println(line) }
	res := e.RunLoaded(ctx, snap, []svcdesc.Loaded{{Desc: d, Origin: svcdesc.OriginHost, File: file}})
	fmt.Println()
	for _, ep := range res.Endpoints {
		fmt.Printf("%s  %s/%s  %s\n", msgs.T(lang, "cli.servicesEndpoint"), ep.Socket(), ep.Protocol, ep.Label)
	}
	for _, u := range res.Upstreams {
		var srv []string
		for _, s := range u.Servers {
			srv = append(srv, s.Socket())
		}
		fmt.Printf("%s  %s → %s\n", msgs.T(lang, "cli.servicesUpstream"), u.Name, strings.Join(srv, ", "))
	}
	for _, f := range res.Findings {
		fmt.Printf("%s  [%s] %s\n", msgs.T(lang, "cli.servicesFinding"), f.Severity, msgs.T(lang, f.TitleKey, f.TitleArgs...))
	}
	for _, s := range res.Sources {
		for i, w := range s.Warnings {
			if i < len(s.WarningRefs) {
				w = msgs.T(lang, s.WarningRefs[i].Key, s.WarningRefs[i].Args...)
			}
			fmt.Printf("%s  %s\n", msgs.T(lang, "cli.servicesWarning"), w)
		}
	}
	return nil
}

func optionsText(opts map[string]string) string {
	if len(opts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+opts[k])
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
