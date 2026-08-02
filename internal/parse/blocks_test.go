package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/model"
)

func splitLines(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func TestNginxBlockEndNested(t *testing.T) {
	text := "server {\n    location / {\n        return 200;\n    }\n}\n"
	end, err := nginxBlockEnd(splitLines(text), 1)
	if err != nil {
		t.Fatalf("nginxBlockEnd: %v", err)
	}
	if end != 5 {
		t.Errorf("конец блока server = %d, ожидалось 5", end)
	}
}

func TestNginxBlockEndSkipsCommentBrace(t *testing.T) {
	text := "server {\n    # a stray { in a comment\n    listen 80;\n}\n"
	end, err := nginxBlockEnd(splitLines(text), 1)
	if err != nil {
		t.Fatalf("nginxBlockEnd: %v", err)
	}
	if end != 4 {
		t.Errorf("конец блока = %d, ожидалось 4 (комментарий с { не должен считаться)", end)
	}
}

func TestNginxBlockEndSkipsQuotedBrace(t *testing.T) {
	text := "server {\n    add_header X-Test \"{ not a brace }\";\n}\n"
	end, err := nginxBlockEnd(splitLines(text), 1)
	if err != nil {
		t.Fatalf("nginxBlockEnd: %v", err)
	}
	if end != 3 {
		t.Errorf("конец блока = %d, ожидалось 3 (скобки в строке не должны считаться)", end)
	}
}

func TestNginxBlockEndCRLF(t *testing.T) {
	text := "server {\r\n    listen 80;\r\n}\r\n"
	end, err := nginxBlockEnd(splitLines(text), 1)
	if err != nil {
		t.Fatalf("nginxBlockEnd: %v", err)
	}
	if end != 3 {
		t.Errorf("конец блока = %d, ожидалось 3", end)
	}
}

func TestNginxBlockEndUnterminated(t *testing.T) {
	text := "server {\n    listen 80;\n"
	if _, err := nginxBlockEnd(splitLines(text), 1); err == nil {
		t.Error("ожидалась ошибка для незакрытого блока")
	}
}

func TestSpliceBlockReplacesMiddle(t *testing.T) {
	text := "one\ntwo\nthree\nfour\nfive"
	got, err := SpliceBlock(text, 2, 3, "TWO\nTHREE")
	if err != nil {
		t.Fatalf("SpliceBlock: %v", err)
	}
	want := "one\nTWO\nTHREE\nfour\nfive"
	if got != want {
		t.Errorf("SpliceBlock = %q, ожидалось %q", got, want)
	}
}

func TestSpliceBlockReplacesLast(t *testing.T) {
	text := "one\ntwo\nthree"
	got, err := SpliceBlock(text, 3, 3, "THREE")
	if err != nil {
		t.Fatalf("SpliceBlock: %v", err)
	}
	if want := "one\ntwo\nTHREE"; got != want {
		t.Errorf("SpliceBlock = %q, ожидалось %q", got, want)
	}
}

func TestSpliceBlockDelete(t *testing.T) {
	text := "one\ntwo\nthree\nfour"
	got, err := SpliceBlock(text, 2, 3, "")
	if err != nil {
		t.Fatalf("SpliceBlock: %v", err)
	}
	if want := "one\nfour"; got != want {
		t.Errorf("SpliceBlock(delete) = %q, ожидалось %q", got, want)
	}
}

func TestSpliceBlockRejectsOutOfRange(t *testing.T) {
	if _, err := SpliceBlock("one\ntwo", 2, 5, "x"); err == nil {
		t.Error("ожидалась ошибка для диапазона за пределами файла")
	}
}

func TestInsertBlockAtEndAppendsToFile(t *testing.T) {
	text := "frontend fe\n    bind *:80\n"
	got, err := InsertBlockAtEnd(text, BlockBackend, "backend be\n    server s1 10.0.0.1:80", 0)
	if err != nil {
		t.Fatalf("InsertBlockAtEnd: %v", err)
	}
	want := "frontend fe\n    bind *:80\n\nbackend be\n    server s1 10.0.0.1:80"
	if got != want {
		t.Errorf("InsertBlockAtEnd = %q, ожидалось %q", got, want)
	}
}

func TestInsertBlockAtEndLocationInsideServer(t *testing.T) {
	text := "server {\n    listen 80;\n}\n"
	// The closing "}" is line 3; a new location must land just before it.
	got, err := InsertBlockAtEnd(text, BlockLocation, "    location /new/ {\n        return 200;\n    }", 3)
	if err != nil {
		t.Fatalf("InsertBlockAtEnd: %v", err)
	}
	want := "server {\n    listen 80;\n    location /new/ {\n        return 200;\n    }\n}\n"
	if got != want {
		t.Errorf("InsertBlockAtEnd(location) = %q, ожидалось %q", got, want)
	}
}

// TestBlocksRoundTrip parses real fixture files, confirms the expected block
// shape, then edits through SpliceBlock/InsertBlockAtEnd and re-parses the
// result to confirm the tree stays well-formed — a splice must never leave a
// file Blocks() can no longer make sense of.
func TestBlocksRoundTrip(t *testing.T) {
	c := collect.NewFixtures("../../fixtures/host")
	const nginxPath = "/etc/nginx/sites-enabled/app.example.com.conf"

	blocks, err := Blocks(c, nginxPath, model.ServiceNginx)
	if err != nil {
		t.Fatalf("Blocks(nginx): %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, ожидалось 2 server{}", len(blocks))
	}
	https := blocks[1]
	if https.Kind != BlockServer || len(https.Children) != 3 {
		t.Fatalf("второй server: kind=%s, детей=%d, ожидалось server с 3 location", https.Kind, len(https.Children))
	}

	raw, err := c.ReadFile(nginxPath)
	if err != nil {
		t.Fatalf("чтение файла: %v", err)
	}
	edited, err := SpliceBlock(string(raw), https.Children[1].StartLine, https.Children[1].EndLine,
		"    location /static/ {\n        proxy_pass http://static_backend;\n        expires 30d;\n    }")
	if err != nil {
		t.Fatalf("SpliceBlock: %v", err)
	}
	if !strings.Contains(edited, "expires 30d;") {
		t.Fatalf("отредактированный текст не содержит новую директиву")
	}

	reparsed, err := blocksFromText(t, edited, model.ServiceNginx)
	if err != nil {
		t.Fatalf("повторный разбор после правки: %v", err)
	}
	if len(reparsed) != 2 || len(reparsed[1].Children) != 3 {
		t.Fatalf("дерево после правки испортилось: %d верхних блоков, %d location во втором",
			len(reparsed), len(reparsed[1].Children))
	}

	const haproxyPath = "/etc/haproxy/haproxy.cfg"
	hapBlocks, err := Blocks(c, haproxyPath, model.ServiceHAProxy)
	if err != nil {
		t.Fatalf("Blocks(haproxy): %v", err)
	}
	var sawFrontend, sawBackend, sawGlobal bool
	for _, b := range hapBlocks {
		switch b.Kind {
		case BlockFrontend:
			sawFrontend = true
		case BlockBackend:
			sawBackend = true
		case BlockGlobal:
			sawGlobal = true
			if b.Editable {
				t.Error("global должен быть Editable=false")
			}
		}
	}
	if !sawFrontend || !sawBackend || !sawGlobal {
		t.Errorf("ожидались frontend, backend и global в %s", haproxyPath)
	}
}

// blocksFromText re-parses edited text as if it were a fresh read — a single
// file written into a throwaway fixtures root, so Blocks() exercises the
// exact same Collector.ReadFile/Open path a real re-fetch after a save would.
func blocksFromText(t *testing.T, text, service string) ([]Block, error) {
	t.Helper()
	const relPath = "etc/nginx/sites-enabled/app.example.com.conf"
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, relPath)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, relPath), []byte(text), 0o644); err != nil {
		t.Fatalf("запись временного файла: %v", err)
	}
	return Blocks(collect.NewFixtures(root), "/"+relPath, service)
}
