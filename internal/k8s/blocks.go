package k8s

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/piqab/nkt/internal/msgs"
)

// Блочный режим редактора YAML: манифест делится на объекты (документы
// между ---), внутри — списки, которые правят чаще всего: контейнеры
// (и init-контейнеры) у всего, что запускает поды, порты Service,
// правила Ingress, ключи ConfigMap. У блока — строки начала и конца в
// тексте: правка, удаление и вставка — замена строк в черновике окна, а
// запись идёт прежним путём (дифф, kubectl diff, apply).

// Block — блок манифеста (по форме — как ConfigBlock у конфигов).
type Block struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Raw       string  `json:"raw"`
	Children  []Block `json:"children,omitempty"`
	Editable  bool    `json:"editable"`
	// Список: что в него добавляется (container, port, rule, key) и с
	// каким отступом элемента.
	Adds   string `json:"adds,omitempty"`
	Indent int    `json:"indent,omitempty"`
}

// listSpec — где в объекте искать список и как звать его элементы.
type listSpec struct {
	path  []string
	kind  string // вид элемента
	name  string // ключ с именем элемента
	title string // подпись списка
}

var podSpecPaths = map[string][]string{
	"Pod":         {"spec"},
	"Deployment":  {"spec", "template", "spec"},
	"StatefulSet": {"spec", "template", "spec"},
	"DaemonSet":   {"spec", "template", "spec"},
	"ReplicaSet":  {"spec", "template", "spec"},
	"Job":         {"spec", "template", "spec"},
	"CronJob":     {"spec", "jobTemplate", "spec", "template", "spec"},
}

func listsFor(kind string) []listSpec {
	var out []listSpec
	if ps, ok := podSpecPaths[kind]; ok {
		out = append(out,
			listSpec{path: append(append([]string{}, ps...), "initContainers"), kind: "container", name: "name", title: "initContainers"},
			listSpec{path: append(append([]string{}, ps...), "containers"), kind: "container", name: "name", title: "containers"},
			listSpec{path: append(append([]string{}, ps...), "volumes"), kind: "volume", name: "name", title: "volumes"},
		)
	}
	switch kind {
	case "Service":
		out = append(out, listSpec{path: []string{"spec", "ports"}, kind: "port", name: "port", title: "ports"})
	case "Ingress":
		out = append(out, listSpec{path: []string{"spec", "rules"}, kind: "rule", name: "host", title: "rules"})
	case "ConfigMap", "Secret":
		out = append(out, listSpec{path: []string{"data"}, kind: "key", title: "data"})
	}
	return out
}

// ManifestBlocks разбирает манифест на блоки.
func ManifestBlocks(content string) ([]Block, error) {
	if len(content) > MaxManifest {
		return nil, msgs.Errorf("k8s.manifestTooBig", MaxManifest>>10)
	}
	lines := strings.Split(content, "\n")
	dec := yaml.NewDecoder(strings.NewReader(content))
	var docs []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, msgs.Errorf("k8s.manifestYAML", err.Error())
		}
		docs = append(docs, &n)
	}
	// Границы документов — строки «---» (с началом и концом текста).
	var seps []int // номера строк (с 1) с ---
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimRight(l, " \t\r"), "---") && strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "---")) == "" {
			seps = append(seps, i+1)
		}
	}
	out := []Block{}
	for i, doc := range docs {
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			continue
		}
		root := doc.Content[0]
		start := firstLine(root)
		end := len(lines)
		for _, s := range seps {
			if s > start {
				end = s - 1
				break
			}
		}
		end = trimEnd(lines, start, end)
		kind := scalar(root, "kind")
		name := scalar(mapValue(root, "metadata"), "name")
		ns := scalar(mapValue(root, "metadata"), "namespace")
		label := strings.TrimSpace(kind + " " + strings.TrimPrefix(ns+"/"+name, "/"))
		if kind == "" {
			// Не объект Kubernetes (values Helm): блоки — ключи верхнего уровня.
			out = append(out, topKeys(lines, root, fmt.Sprintf("d%d", i), end)...)
			continue
		}
		obj := Block{ID: fmt.Sprintf("d%d", i), Kind: "object", Name: label, StartLine: start, EndLine: end, Raw: joinLines(lines, start, end), Editable: true}
		for j, ls := range listsFor(kind) {
			node := pathNode(root, ls.path)
			if node == nil {
				continue
			}
			lb := listBlock(lines, root, ls, node, fmt.Sprintf("%s.l%d", obj.ID, j), end)
			if lb != nil {
				obj.Children = append(obj.Children, *lb)
			}
		}
		out = append(out, obj)
	}
	return out, nil
}

// listBlock — блок списка с элементами.
func listBlock(lines []string, root *yaml.Node, ls listSpec, node *yaml.Node, id string, docEnd int) *Block {
	keyLine := keyLineOf(root, ls.path)
	if keyLine == 0 {
		return nil
	}
	keyIndent := indentOf(lines[keyLine-1])
	end := blockEnd(lines, keyLine, keyIndent, docEnd)
	b := &Block{ID: id, Kind: "list", Name: ls.title, StartLine: keyLine, EndLine: end, Raw: joinLines(lines, keyLine, end), Editable: true, Adds: ls.kind}
	switch node.Kind {
	case yaml.SequenceNode:
		b.Indent = keyIndent + 2
		for k, item := range node.Content {
			s := item.Line
			if s <= 0 || s > len(lines) {
				continue
			}
			dash := indentOf(lines[s-1])
			if k == 0 {
				b.Indent = dash
			}
			e := blockEnd(lines, s, dash, end)
			name := ""
			if item.Kind == yaml.MappingNode && ls.name != "" {
				name = scalar(item, ls.name)
			}
			b.Children = append(b.Children, Block{ID: fmt.Sprintf("%s.i%d", id, k), Kind: ls.kind, Name: name, StartLine: s, EndLine: e, Raw: joinLines(lines, s, e), Editable: true})
		}
	case yaml.MappingNode:
		b.Indent = keyIndent + 2
		for k := 0; k+1 < len(node.Content); k += 2 {
			key := node.Content[k]
			s := key.Line
			if s <= 0 || s > len(lines) {
				continue
			}
			ind := indentOf(lines[s-1])
			if k == 0 {
				b.Indent = ind
			}
			e := blockEnd(lines, s, ind, end)
			b.Children = append(b.Children, Block{ID: fmt.Sprintf("%s.i%d", id, k/2), Kind: ls.kind, Name: key.Value, StartLine: s, EndLine: e, Raw: joinLines(lines, s, e), Editable: true})
		}
	default:
		return nil
	}
	return b
}

// blockEnd — последняя строка блока, начатого на start с отступом indent:
// дальше идут строки с большим отступом (и пустые между ними).
func blockEnd(lines []string, start, indent, limit int) int {
	end := start
	for i := start + 1; i <= limit && i <= len(lines); i++ {
		l := lines[i-1]
		if strings.TrimSpace(l) == "" {
			continue
		}
		ind := indentOf(l)
		// Элемент списка без отступа под ключом («key:\n- a») — тоже его.
		if ind > indent || (ind == indent && strings.HasPrefix(strings.TrimSpace(l), "- ") && !strings.HasPrefix(strings.TrimSpace(lines[start-1]), "- ")) {
			end = i
			continue
		}
		break
	}
	return end
}

func trimEnd(lines []string, start, end int) int {
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}

func indentOf(l string) int { return len(l) - len(strings.TrimLeft(l, " ")) }

func joinLines(lines []string, start, end int) string {
	if start < 1 || end < start || end > len(lines) {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n") + "\n"
}

func firstLine(n *yaml.Node) int {
	if n.Kind == yaml.MappingNode && len(n.Content) > 0 {
		return n.Content[0].Line
	}
	return n.Line
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node, key string) string {
	if v := mapValue(n, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}

func pathNode(n *yaml.Node, path []string) *yaml.Node {
	cur := n
	for _, p := range path {
		cur = mapValue(cur, p)
		if cur == nil {
			return nil
		}
	}
	return cur
}

// keyLineOf — строка ключа последнего элемента пути.
func keyLineOf(n *yaml.Node, path []string) int {
	cur := n
	for i, p := range path {
		if cur == nil || cur.Kind != yaml.MappingNode {
			return 0
		}
		var next *yaml.Node
		for k := 0; k+1 < len(cur.Content); k += 2 {
			if cur.Content[k].Value == p {
				if i == len(path)-1 {
					return cur.Content[k].Line
				}
				next = cur.Content[k+1]
			}
		}
		cur = next
	}
	return 0
}

// topKeys — ключи верхнего уровня документа как блоки.
func topKeys(lines []string, root *yaml.Node, id string, docEnd int) []Block {
	var out []Block
	for k := 0; k+1 < len(root.Content); k += 2 {
		key := root.Content[k]
		s := key.Line
		if s <= 0 || s > len(lines) {
			continue
		}
		e := trimEnd(lines, s, blockEnd(lines, s, indentOf(lines[s-1]), docEnd))
		out = append(out, Block{ID: fmt.Sprintf("%s.k%d", id, k/2), Kind: "key", Name: key.Value, StartLine: s, EndLine: e, Raw: joinLines(lines, s, e), Editable: true})
	}
	return out
}
