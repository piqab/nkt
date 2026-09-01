package parse

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	crossplane "github.com/nginxinc/nginx-go-crossplane"

	"github.com/piqab/nkt/internal/collect"
	"github.com/piqab/nkt/internal/model"
)

// BlockKind identifies the kind of structural block a Block describes.
type BlockKind string

// The block kinds addressable for CRUD in v1. nginx server/location nest;
// upstream does not. haproxy sections are always flat — global and defaults
// are viewable and editable but not creatable/deletable (see Block.Editable).
// docker-compose service is flat too — one entry per key under services:.
const (
	BlockServer   BlockKind = "server"
	BlockLocation BlockKind = "location"
	BlockUpstream BlockKind = "upstream"
	BlockFrontend BlockKind = "frontend"
	BlockBackend  BlockKind = "backend"
	BlockListen   BlockKind = "listen"
	BlockGlobal   BlockKind = "global"
	BlockDefaults BlockKind = "defaults"
	BlockService  BlockKind = "service"
	// BlockSite is one Caddyfile site block ("example.com { ... }") — always
	// flat in v1, same as haproxy's sections: a handle{}/route{} nested
	// inside stays part of its site's raw text rather than its own
	// addressable block.
	BlockSite BlockKind = "site"
)

// Block is one structural unit of a single config file — a nginx server{}/
// location{}/upstream{} or a haproxy frontend/backend/listen/global/defaults
// section — addressed by exact source line range so a caller can splice it
// out of the raw file text without reparsing and rebuilding the whole file
// (neither nginx-go-crossplane nor haproxytech/config-parser preserves
// comments/formatting/section order on a full round trip).
type Block struct {
	ID        string    `json:"id"` // "kind:startLine", recomputed on every List call
	Kind      BlockKind `json:"kind"`
	Name      string    `json:"name"`       // server_name/upstream/frontend/backend/listen name; "" for a nameless server or location match
	StartLine int       `json:"start_line"` // 1-based, inclusive
	EndLine   int       `json:"end_line"`   // 1-based, inclusive
	Raw       string    `json:"raw"`        // exact source text of [StartLine, EndLine]
	Children  []Block   `json:"children,omitempty"`
	// Editable is false for haproxy global/defaults: create and delete are
	// refused for these singleton sections (there is normally exactly one of
	// each, and removing either breaks the config), but viewing and updating
	// their raw text is still a plain block update.
	Editable bool `json:"editable"`
}

// Blocks builds the block tree for one config file. It reparses only that
// file (SingleFile for nginx; scanHAProxySections is already per-file), so it
// never follows nginx include directives — CRUD is deliberately scoped to one
// file at a time.
func Blocks(c collect.Collector, path, service string) ([]Block, error) {
	switch service {
	case model.ServiceNginx:
		return nginxBlocks(c, path)
	case model.ServiceHAProxy:
		return haproxyBlocks(c, path)
	case model.ServiceDocker:
		return dockerBlocks(c, path)
	case model.ServiceCaddy:
		return caddyBlocks(c, path)
	default:
		return nil, fmt.Errorf("для сервиса %q дерево блоков не поддерживается", service)
	}
}

// --------------------------------------------------------------------- nginx

func nginxBlocks(c collect.Collector, path string) ([]Block, error) {
	raw, err := c.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение файла: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	payload, err := crossplane.Parse(path, &crossplane.ParseOptions{
		Open:                      func(name string) (io.ReadCloser, error) { return c.Open(name) },
		SingleFile:                true,
		SkipDirectiveArgsCheck:    true,
		SkipDirectiveContextCheck: true,
	})
	if err != nil {
		return nil, fmt.Errorf("разбор %s: %w", path, err)
	}

	var out []Block
	for _, cfg := range payload.Config {
		blocks, err := walkNginxBlocks(cfg.Parsed, lines)
		if err != nil {
			return nil, err
		}
		out = append(out, blocks...)
	}
	return out, nil
}

// walkNginxBlocks collects server{} and upstream{} blocks anywhere in the
// tree (including nested inside http{}/stream{}, which are walked through
// transparently — they are not blocks in their own right for v1).
func walkNginxBlocks(dirs crossplane.Directives, lines []string) ([]Block, error) {
	var out []Block
	for _, d := range dirs {
		switch d.Directive {
		case "upstream":
			if len(d.Args) == 0 {
				continue
			}
			b, err := newNginxBlock(BlockUpstream, d.Args[0], d.Line, lines)
			if err != nil {
				return nil, err
			}
			out = append(out, b)
			continue

		case "server":
			// "server" inside upstream{} is a pool member, not a block — but
			// upstream's case above never recurses into d.Block, so this case
			// is never reached for pool-member "server" lines in the first place.
			b, err := newNginxBlock(BlockServer, nginxServerName(d), d.Line, lines)
			if err != nil {
				return nil, err
			}
			children, err := walkNginxLocations(d.Block, lines)
			if err != nil {
				return nil, err
			}
			b.Children = children
			out = append(out, b)
			continue
		}
		if d.IsBlock() {
			children, err := walkNginxBlocks(d.Block, lines)
			if err != nil {
				return nil, err
			}
			out = append(out, children...)
		}
	}
	return out, nil
}

// walkNginxLocations collects only the direct location{} children of a
// server{} — a location nested inside another location stays part of its
// parent's raw text in v1, not separately addressable.
func walkNginxLocations(dirs crossplane.Directives, lines []string) ([]Block, error) {
	var out []Block
	for _, d := range dirs {
		if d.Directive != "location" {
			continue
		}
		b, err := newNginxBlock(BlockLocation, strings.Join(d.Args, " "), d.Line, lines)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func nginxServerName(d *crossplane.Directive) string {
	var names []string
	for _, inner := range d.Block {
		if inner.Directive == "server_name" {
			names = append(names, inner.Args...)
		}
	}
	return strings.Join(names, ", ")
}

func newNginxBlock(kind BlockKind, name string, startLine int, lines []string) (Block, error) {
	end, err := nginxBlockEnd(lines, startLine)
	if err != nil {
		return Block{}, fmt.Errorf("%s %q (строка %d): %w", kind, name, startLine, err)
	}
	return Block{
		ID:        fmt.Sprintf("%s:%d", kind, startLine),
		Kind:      kind,
		Name:      name,
		StartLine: startLine,
		EndLine:   end,
		Raw:       strings.Join(lines[startLine-1:end], "\n"),
		Editable:  true,
	}, nil
}

// nginxBlockEnd finds the line of the "}" that closes the block opened on
// startLine, by depth-counting braces across the raw file text. crossplane
// gives no end-of-block position at all, so this has to scan text directly —
// skipping "#" comments and quoted-string contents (nginx allows braces
// inside a quoted directive argument, e.g. add_header's value) so those
// don't throw the depth count off.
func nginxBlockEnd(lines []string, startLine int) (int, error) {
	if startLine < 1 || startLine > len(lines) {
		return 0, fmt.Errorf("строка %d вне диапазона файла (%d строк)", startLine, len(lines))
	}
	depth := 0
	var quote byte // 0 when not inside a quoted string
	for i := startLine - 1; i < len(lines); i++ {
		line := lines[i]
		for j := 0; j < len(line); j++ {
			ch := line[j]
			if quote != 0 {
				if ch == '\\' {
					j++ // the escaped character is never special, skip it too
					continue
				}
				if ch == quote {
					quote = 0
				}
				continue
			}
			switch ch {
			case '#':
				j = len(line) // rest of the line is a comment
			case '"', '\'':
				quote = ch
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return i + 1, nil
				}
			}
		}
	}
	return 0, fmt.Errorf("не удалось определить конец блока, начатого в строке %d", startLine)
}

// --------------------------------------------------------------------- caddy

// caddySection is one top-level Caddyfile site block ("example.com { ... }"),
// found by brace-depth scanning — reusing nginxBlockEnd as-is, since it is
// already syntax-agnostic (brace counting that skips "#" comments and
// quoted-string contents) and Caddyfile blocks are delimited the same way
// nginx's are. Lines holds everything strictly between the header and the
// closing "}", nested blocks (handle{}, route{}, ...) included but
// flattened — Caddy has no other structure worth addressing in v1.
type caddySection struct {
	Addr  string // raw site address list, e.g. "example.com, www.example.com"
	Line  int    // 1-based line of the opening "addr {" line
	End   int    // 1-based line of the matching closing "}"
	Lines []string
}

// caddySiteHeaderRe matches a top-level "<addresses> {" line. The bare
// global options block ("{" alone, no address before it) does not match —
// \S.* requires at least one non-whitespace character ahead of the brace —
// which is exactly right: it has no site address to build an Endpoint from.
var caddySiteHeaderRe = regexp.MustCompile(`^(\S.*)\{\s*$`)

func scanCaddySites(lines []string) ([]caddySection, error) {
	var out []caddySection
	for i, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // indented — nested inside another block, not top-level
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		m := caddySiteHeaderRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		addr := strings.TrimSpace(m[1])
		if addr == "" {
			continue
		}
		startLine := i + 1
		end, err := nginxBlockEnd(lines, startLine)
		if err != nil {
			return nil, fmt.Errorf("сайт %q (строка %d): %w", addr, startLine, err)
		}
		var body []string
		if end > startLine+1 {
			body = lines[startLine : end-1]
		}
		out = append(out, caddySection{Addr: addr, Line: startLine, End: end, Lines: body})
	}
	return out, nil
}

func caddyBlocks(c collect.Collector, path string) ([]Block, error) {
	raw, err := c.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение файла: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	sections, err := scanCaddySites(lines)
	if err != nil {
		return nil, err
	}
	out := make([]Block, 0, len(sections))
	for _, sec := range sections {
		out = append(out, Block{
			ID:        fmt.Sprintf("%s:%d", BlockSite, sec.Line),
			Kind:      BlockSite,
			Name:      sec.Addr,
			StartLine: sec.Line,
			EndLine:   sec.End,
			Raw:       strings.Join(lines[sec.Line-1:sec.End], "\n"),
			Editable:  true,
		})
	}
	return out, nil
}

// ------------------------------------------------------------------- haproxy

var haproxyBlockKinds = map[string]BlockKind{
	"frontend": BlockFrontend,
	"backend":  BlockBackend,
	"listen":   BlockListen,
	"global":   BlockGlobal,
	"defaults": BlockDefaults,
}

// haproxyBlocks reuses scanHAProxySections (haproxy.go) almost as-is — it
// already scans raw text into per-section {Kind, Name, Line, Lines}, which is
// exactly the line-span information block CRUD needs.
func haproxyBlocks(c collect.Collector, path string) ([]Block, error) {
	raw, err := c.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение файла: %w", err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.Split(text, "\n")

	out := make([]Block, 0, 8)
	for _, sec := range scanHAProxySections(text) {
		kind, ok := haproxyBlockKinds[sec.Kind]
		if !ok {
			continue // resolvers/peers/cache/... are out of scope for v1
		}
		// sec.Lines holds every raw line after the header up to (not
		// including) the next section header or EOF, so the section's last
		// line is Line+len(Lines) — trim any trailing blank separator lines
		// so a block's Raw text doesn't carry the gap before the next section.
		end := sec.Line + len(sec.Lines)
		if end > len(lines) {
			end = len(lines)
		}
		for end > sec.Line && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		out = append(out, Block{
			ID:        fmt.Sprintf("%s:%d", kind, sec.Line),
			Kind:      kind,
			Name:      sec.Name,
			StartLine: sec.Line,
			EndLine:   end,
			Raw:       strings.Join(lines[sec.Line-1:end], "\n"),
			Editable:  kind != BlockGlobal && kind != BlockDefaults,
		})
	}
	return out, nil
}

// --------------------------------------------------------------- compose

// dockerBlocks reads one compose file's services: entries as flat blocks —
// docker-compose.yml has no other structure worth addressing for v1.
func dockerBlocks(c collect.Collector, path string) ([]Block, error) {
	raw, err := c.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение файла: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	return composeServiceBlocks(lines), nil
}

// composeKeyAt reports the indent and key name of a YAML "key:" mapping line
// (nothing after the colon on the same line), which is the only line shape
// that can open a compose service — "  web:" — or close one out by starting
// a shallower-indent sibling like "networks:". Anything else (a "key: value"
// line, a "- item" list entry, a comment, a blank line) is not a key line.
func composeKeyAt(line string) (indent int, key string, ok bool) {
	trimmedRight := strings.TrimRight(line, " \t\r")
	if trimmedRight == "" {
		return 0, "", false
	}
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	content := trimmedRight[i:]
	if content == "" || strings.HasPrefix(content, "#") || strings.HasPrefix(content, "-") ||
		!strings.HasSuffix(content, ":") {
		return 0, "", false
	}
	key = strings.TrimSuffix(content, ":")
	if key == "" {
		return 0, "", false
	}
	return i, key, true
}

// composeServicesRe matches the top-level services: key either bare (with
// block-style children below) or as an explicit empty flow mapping —
// "services:" alone parses to null in YAML, which `docker compose` rejects
// ("services must be a mapping"), so a freshly created, still-empty compose
// file is written as "services: {}" instead; this needs to recognise both.
var composeServicesRe = regexp.MustCompile(`^services:\s*(\{\s*\})?$`)

// composeServicesLine finds the top-level services: key (0-based line index
// into lines, -1 if the file has none).
func composeServicesLine(lines []string) int {
	for i, line := range lines {
		if composeServicesRe.MatchString(strings.TrimRight(line, " \t\r")) {
			return i
		}
	}
	return -1
}

// composeServiceBlocks scans the raw lines under services: by indentation
// depth — YAML has no braces, so a service's extent is "every line indented
// deeper than its own key, until the next line at the same or shallower
// indent" (the next service, or a sibling top-level key like volumes:).
func composeServiceBlocks(lines []string) []Block {
	servicesIdx := composeServicesLine(lines)
	if servicesIdx < 0 {
		return nil
	}

	childIndent := -1
	for i := servicesIdx + 1; i < len(lines); i++ {
		indent, _, ok := composeKeyAt(lines[i])
		if !ok {
			continue
		}
		if indent == 0 {
			break // a sibling top-level key immediately follows — no services yet
		}
		childIndent = indent
		break
	}
	if childIndent < 0 {
		return nil // "services:" with nothing under it
	}

	var out []Block
	start, name := -1, ""
	closeBlock := func(endExclusive int) {
		if start < 0 {
			return
		}
		end := endExclusive
		for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		out = append(out, Block{
			ID: fmt.Sprintf("%s:%d", BlockService, start+1), Kind: BlockService, Name: name,
			StartLine: start + 1, EndLine: end, Raw: strings.Join(lines[start:end], "\n"), Editable: true,
		})
		start = -1
	}

	for i := servicesIdx + 1; i <= len(lines); i++ {
		if i == len(lines) {
			closeBlock(i)
			break
		}
		indent, key, ok := composeKeyAt(lines[i])
		switch {
		case ok && indent == childIndent:
			closeBlock(i)
			start, name = i, key
		case ok && indent < childIndent:
			closeBlock(i)
			return out // left services: entirely — a sibling top-level key
		}
	}
	return out
}

// composeInsertLine finds where a new service belongs: right after the last
// existing one (with a blank separator line, matching how compose files are
// normally written), or right after "services:" itself when there are none
// yet (no separator needed there). Returns a 1-based line to insert before.
func composeInsertLine(lines []string) (line int, gapBefore bool, err error) {
	servicesIdx := composeServicesLine(lines)
	if servicesIdx < 0 {
		return 0, false, fmt.Errorf("в файле нет ключа services:")
	}
	if existing := composeServiceBlocks(lines); len(existing) > 0 {
		return existing[len(existing)-1].EndLine + 1, true, nil
	}
	return servicesIdx + 2, false, nil
}

// ----------------------------------------------------------------- splicing

// SpliceBlock replaces the inclusive line range [startLine, endLine] of
// fileText with newText, leaving every other line byte-for-byte untouched.
// Line numbers are 1-based and are not re-normalised for \r\n — "\r" is never
// a line separator, so an index computed against a \r\n-normalised copy of
// the same text lands on the same line here.
func SpliceBlock(fileText string, startLine, endLine int, newText string) (string, error) {
	lines := strings.Split(fileText, "\n")
	if startLine < 1 || endLine < startLine || endLine > len(lines) {
		return "", fmt.Errorf("диапазон строк %d..%d вне файла (%d строк)", startLine, endLine, len(lines))
	}
	out := make([]string, 0, len(lines))
	out = append(out, lines[:startLine-1]...)
	if trimmed := strings.TrimRight(newText, "\n"); trimmed != "" {
		out = append(out, strings.Split(trimmed, "\n")...)
	}
	out = append(out, lines[endLine:]...)
	return strings.Join(out, "\n"), nil
}

// InsertBlockAtEnd appends newText as a new block. A nginx location{} is
// inserted just before its parent server{}'s closing "}" (parentEndLine,
// taken from a Block the caller already fetched); a docker-compose service
// is inserted right after the last existing one under services: (found
// internally — YAML indentation means "the end of the file" is not a valid
// position once volumes:/networks: follow); every other kind is appended at
// the end of the file, since a top-level nginx server/upstream or a haproxy
// section is always valid wherever it lands in the file.
func InsertBlockAtEnd(fileText string, kind BlockKind, newText string, parentEndLine int) (string, error) {
	lines := strings.Split(fileText, "\n")
	block := strings.Split(strings.TrimRight(newText, "\n"), "\n")

	switch kind {
	case BlockLocation:
		if parentEndLine < 1 || parentEndLine > len(lines) {
			return "", fmt.Errorf("родительский блок вне диапазона файла")
		}
		return insertBefore(lines, parentEndLine, block), nil

	case BlockService:
		// A freshly created, still-empty compose file spells its services:
		// key as "services: {}" (see composeServicesRe) — valid YAML, but a
		// flow-empty value and an indented block child can't share the same
		// key, so the first service inserted has to rewrite that line to
		// bare "services:" first.
		if idx := composeServicesLine(lines); idx >= 0 && strings.TrimRight(lines[idx], " \t\r") != "services:" {
			lines = append([]string{}, lines...)
			lines[idx] = "services:"
		}
		target, gap, err := composeInsertLine(lines)
		if err != nil {
			return "", err
		}
		toInsert := block
		if gap {
			toInsert = append([]string{""}, block...)
		}
		return insertBefore(lines, target, toInsert), nil
	}

	out := append([]string{}, lines...)
	if len(out) > 0 && out[len(out)-1] != "" {
		out = append(out, "") // one blank line of separation from existing content
	}
	out = append(out, block...)
	return strings.Join(out, "\n"), nil
}

// insertBefore splices block just before the 1-based line "at" (pushing that
// line and everything after it down), leaving everything before it untouched.
func insertBefore(lines []string, at int, block []string) string {
	out := make([]string, 0, len(lines)+len(block))
	out = append(out, lines[:at-1]...)
	out = append(out, block...)
	out = append(out, lines[at-1:]...)
	return strings.Join(out, "\n")
}
