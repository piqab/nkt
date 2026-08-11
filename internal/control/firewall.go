package control

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/config"
	"github.com/althq/netknownsthat/internal/store"
)

// FirewallManager changes firewall rules through ufw.
//
// Only ufw is written to. Editing raw iptables from a web UI is a good way to
// lock yourself out of a host, and ufw gives an auditable, reversible surface.
type FirewallManager struct {
	cfg *config.Config
	c   collect.Collector
	db  *store.DB
}

// NewFirewallManager builds the firewall control plane.
func NewFirewallManager(cfg *config.Config, c collect.Collector, db *store.DB) *FirewallManager {
	return &FirewallManager{cfg: cfg, c: c, db: db}
}

// RuleSpec describes a rule to add.
type RuleSpec struct {
	Action   string `json:"action"` // allow | deny | reject | limit
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // tcp | udp
	From     string `json:"from"`     // IP or CIDR; empty means anywhere
	Comment  string `json:"comment"`
}

var (
	cidrRe    = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}(/\d{1,2})?$`)
	ipv6Re    = regexp.MustCompile(`^[0-9a-fA-F:]+(/\d{1,3})?$`)
	commentRe = regexp.MustCompile(`^[\p{L}\p{N} .,:/_@()-]{0,80}$`)
)

// Validate rejects anything that would be unsafe or meaningless to pass to ufw.
func (r RuleSpec) Validate() error {
	switch r.Action {
	case "allow", "deny", "reject", "limit":
	default:
		return fmt.Errorf("недопустимое действие: %q", r.Action)
	}
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("порт вне диапазона 1..65535: %d", r.Port)
	}
	switch r.Protocol {
	case "tcp", "udp":
	default:
		return fmt.Errorf("протокол должен быть tcp или udp, получено %q", r.Protocol)
	}
	if r.From != "" && !cidrRe.MatchString(r.From) && !ipv6Re.MatchString(r.From) {
		return fmt.Errorf("источник должен быть IP или CIDR, получено %q", r.From)
	}
	if !commentRe.MatchString(r.Comment) {
		return fmt.Errorf("комментарий содержит недопустимые символы или слишком длинный")
	}
	return nil
}

// argv builds the ufw command line for the rule.
func (r RuleSpec) argv() []string {
	args := []string{r.Action}
	if r.From != "" {
		args = append(args, "from", r.From, "to", "any", "port", strconv.Itoa(r.Port), "proto", r.Protocol)
	} else {
		args = append(args, fmt.Sprintf("%d/%s", r.Port, r.Protocol))
	}
	if r.Comment != "" {
		args = append(args, "comment", r.Comment)
	}
	return args
}

// AddRule appends a rule to ufw.
func (f *FirewallManager) AddRule(ctx context.Context, user string, spec RuleSpec) (collect.CommandResult, error) {
	if err := spec.Validate(); err != nil {
		return collect.CommandResult{}, err
	}
	args := spec.argv()
	res, err := f.c.Run(ctx, "ufw", args...)

	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	f.db.Audit(ctx, user, "firewall.add", fmt.Sprintf("%d/%s", spec.Port, spec.Protocol), outcome,
		map[string]any{"argv": append([]string{"ufw"}, args...), "output": strings.TrimSpace(res.Output())})

	if err != nil {
		return res, err
	}
	if !res.OK() {
		return res, fmt.Errorf("ufw вернул код %d: %s", res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return res, nil
}

// NumberedRule is one entry of `ufw status numbered`.
type NumberedRule struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

var numberedRe = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.*)$`)

// NumberedRules lists ufw rules with the indices delete expects.
func (f *FirewallManager) NumberedRules(ctx context.Context) ([]NumberedRule, error) {
	res, err := f.c.Run(ctx, "ufw", "status", "numbered")
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, fmt.Errorf("ufw status numbered: код %d", res.ExitCode)
	}
	// Zero custom ufw rules is a common, entirely valid state (ufw inactive,
	// or active with only its own default policy) — out must stay a real
	// empty slice, not nil, since the frontend renders {"rules": rules}
	// with a bare .map, and encoding/json marshals a nil slice as `null`.
	out := []NumberedRule{}
	for _, line := range strings.Split(strings.ReplaceAll(res.Stdout, "\r\n", "\n"), "\n") {
		m := numberedRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		out = append(out, NumberedRule{Number: n, Text: strings.TrimSpace(m[2])})
	}
	return out, nil
}

// DeleteRule removes a rule by its ufw index. The caller must pass the text it
// saw next to that index, and it is compared against the live list first: rule
// numbers shift after every change, and deleting the wrong one can cut off SSH.
func (f *FirewallManager) DeleteRule(ctx context.Context, user string, number int, expected string) (collect.CommandResult, error) {
	if number < 1 {
		return collect.CommandResult{}, fmt.Errorf("номер правила должен быть положительным")
	}
	rules, err := f.NumberedRules(ctx)
	if err != nil {
		return collect.CommandResult{}, err
	}
	var found *NumberedRule
	for i := range rules {
		if rules[i].Number == number {
			found = &rules[i]
			break
		}
	}
	if found == nil {
		return collect.CommandResult{}, fmt.Errorf("правила с номером %d нет", number)
	}
	if expected != "" && !strings.EqualFold(strings.Join(strings.Fields(found.Text), " "),
		strings.Join(strings.Fields(expected), " ")) {
		return collect.CommandResult{}, fmt.Errorf(
			"правило №%d изменилось с момента загрузки страницы (сейчас: %q), удаление отменено",
			number, found.Text)
	}

	res, err := f.c.Run(ctx, "ufw", "--force", "delete", strconv.Itoa(number))
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	f.db.Audit(ctx, user, "firewall.delete", strconv.Itoa(number), outcome,
		map[string]any{"rule": found.Text, "output": strings.TrimSpace(res.Output())})

	if err != nil {
		return res, err
	}
	if !res.OK() {
		return res, fmt.Errorf("ufw вернул код %d: %s", res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return res, nil
}

// Reload re-applies the ufw ruleset.
func (f *FirewallManager) Reload(ctx context.Context, user string) (collect.CommandResult, error) {
	res, err := f.c.Run(ctx, "ufw", "reload")
	outcome := "ok"
	if err != nil || !res.OK() {
		outcome = "error"
	}
	f.db.Audit(ctx, user, "firewall.reload", "ufw", outcome, strings.TrimSpace(res.Output()))
	if err != nil {
		return res, err
	}
	if !res.OK() {
		return res, fmt.Errorf("ufw reload: код %d: %s", res.ExitCode, strings.TrimSpace(res.Output()))
	}
	return res, nil
}
