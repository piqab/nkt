package api

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/deploy"
)

// Образы стека глазами compose (после подстановки .env): имя, которое
// движок не примет, pull_policy, при которой pull не обновит образ, и
// сборка из исходников, которых на хосте нет.

// composeService — сервис из compose config --format json.
type composeService struct {
	Name         string
	Image        string
	PullPolicy   string
	Build        bool
	BuildContext string
}

// parseComposeServices — сервисы из вывода compose config --format json
// (не JSON — пусто: старый compose или podman-compose).
func parseComposeServices(text string) []composeService {
	var doc struct {
		Services map[string]struct {
			Image      string          `json:"image"`
			PullPolicy string          `json:"pull_policy"`
			Build      json.RawMessage `json:"build"`
		} `json:"services"`
	}
	if json.Unmarshal([]byte(text), &doc) != nil {
		return nil
	}
	out := make([]composeService, 0, len(doc.Services))
	for name, s := range doc.Services {
		cs := composeService{Name: name, Image: strings.TrimSpace(s.Image), PullPolicy: strings.ToLower(s.PullPolicy)}
		if len(s.Build) > 0 && string(s.Build) != "null" {
			cs.Build = true
			var b struct {
				Context string `json:"context"`
			}
			if json.Unmarshal(s.Build, &b) != nil {
				_ = json.Unmarshal(s.Build, &b.Context) // build: строкой
			}
			cs.BuildContext = b.Context
		}
		out = append(out, cs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// composeServices — сервисы стека по compose config --format json.
func composeServices(ctx context.Context, c collect.Collector, engine string, args []string) []composeService {
	res, err := c.RunTimeout(ctx, time.Minute, engine, append(append([]string{}, args...), "config", "--format", "json")...)
	if err != nil || !res.OK() {
		return nil
	}
	return parseComposeServices(res.Stdout)
}

// ComposeImageIssue — что помешает получить образ сервиса.
type ComposeImageIssue struct {
	Service string `json:"service"`
	Image   string `json:"image,omitempty"`
	// Kind — scheme, nohost, bad (имя образа движок не примет); stale
	// (pull_policy не always, а образ уже на хосте — pull его не обновит);
	// never (pull_policy: never, образа нет); build_outside (build: из
	// каталога, которого на хосте нет); build_policy (то же при
	// pull_policy: build — образ только собирается).
	Kind    string `json:"kind"`
	Policy  string `json:"policy,omitempty"`
	Context string `json:"context,omitempty"`
}

// pullKeepsLocal — pull_policy, при которой compose pull не трогает образ,
// уже скачанный на хост.
func pullKeepsLocal(policy string) bool {
	return policy == "missing" || policy == "if_not_present" || policy == "never"
}

// composeImageIssues — проблемы образов стека; bad — имена, которые
// движок не примет (их незачем проверять в registry). base — каталог
// стека: build из-за его пределов на хосте не собрать.
func composeImageIssues(ctx context.Context, c collect.Collector, engine, base string, svcs []composeService) (issues []ComposeImageIssue, bad map[string]bool) {
	bad = map[string]bool{}
	for _, s := range svcs {
		if s.Image != "" {
			if p := deploy.ImageRefProblem(s.Image); p != "" {
				bad[s.Image] = true
				issues = append(issues, ComposeImageIssue{Service: s.Name, Image: s.Image, Kind: p})
				continue
			}
			if pullKeepsLocal(s.PullPolicy) {
				present := imagePresent(ctx, c, engine, s.Image)
				switch {
				case present:
					issues = append(issues, ComposeImageIssue{Service: s.Name, Image: s.Image, Kind: "stale", Policy: s.PullPolicy})
				case s.PullPolicy == "never":
					issues = append(issues, ComposeImageIssue{Service: s.Name, Image: s.Image, Kind: "never", Policy: s.PullPolicy})
				}
			}
		}
		if s.Build {
			if rel, ok := buildOutside(c, base, s.BuildContext); ok {
				kind := "build_outside"
				if s.PullPolicy == "build" {
					kind = "build_policy"
				}
				issues = append(issues, ComposeImageIssue{Service: s.Name, Image: s.Image, Kind: kind, Policy: s.PullPolicy, Context: rel})
			}
		}
	}
	return issues, bad
}

// buildOutside — context сборки вне каталога стека или его нет на хосте
// (исходники в стек не попадают); rel — путь от каталога стека.
func buildOutside(c collect.Collector, base, context string) (string, bool) {
	if context == "" || strings.Contains(context, "://") || strings.HasPrefix(context, "git@") {
		return "", false // удалённый context: compose скачает его сам
	}
	rel, err := filepath.Rel(base, context)
	if err != nil {
		return context, true
	}
	if rel == ".." || strings.HasPrefix(rel, "../") || !c.Exists(context) {
		return rel, true
	}
	return "", false
}

// imagePresent — образ уже скачан на хост.
func imagePresent(ctx context.Context, c collect.Collector, engine, img string) bool {
	res, err := c.RunTimeout(ctx, 30*time.Second, engine, "image", "inspect", "--format", "{{.Id}}", img)
	return err == nil && res.OK()
}
