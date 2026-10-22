package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// runMCP — `nkt mcp`: мост stdio → MCP хаба для клиентов, которые
// запускают сервер командой (Claude Desktop и т. п.). Каждая строка stdin —
// сообщение JSON-RPC; оно уходит POST'ом на NKT_HUB_URL/api/mcp с токеном
// NKT_API_TOKEN, ответ — строкой в stdout. Токен — только из окружения,
// не из аргументов (их видно в списке процессов). NKT_HUB_CA — PEM, если
// у хаба самоподписанный сертификат.
func runMCP(in io.Reader, out io.Writer) error {
	base := strings.TrimRight(os.Getenv("NKT_HUB_URL"), "/")
	token := os.Getenv("NKT_API_TOKEN")
	u, err := url.Parse(base)
	if base == "" || token == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return msgs.Errorf("cli.mcpEnv")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if ca := os.Getenv("NKT_HUB_CA"); ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return msgs.Errorf("cli.mcpCA", ca)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	// Задание с wait ждёт до часа.
	client := &http.Client{Transport: transport, Timeout: 65 * time.Minute}
	endpoint := base + "/api/mcp"
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(line, &msg)
		reply := mcpForward(client, endpoint, token, line)
		if len(msg.ID) == 0 { // уведомление: ответа нет
			continue
		}
		if reply == nil {
			continue
		}
		if !json.Valid(reply.body) {
			reply.body = mcpBridgeError(msg.ID, reply.err)
		}
		_, _ = w.Write(bytes.TrimSpace(reply.body))
		_ = w.WriteByte('\n')
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return sc.Err()
}

type mcpReply struct {
	body []byte
	err  string
}

func mcpForward(client *http.Client, endpoint, token string, line []byte) *mcpReply {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(line))
	if err != nil {
		return &mcpReply{err: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	if lang := os.Getenv("LANG"); strings.HasPrefix(lang, "ru") {
		req.Header.Set("Accept-Language", "ru")
	}
	resp, err := client.Do(req)
	if err != nil {
		return &mcpReply{err: err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == http.StatusAccepted {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return &mcpReply{err: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	return &mcpReply{body: body}
}

func mcpBridgeError(id json.RawMessage, text string) []byte {
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": "nkt hub: " + text}})
	return raw
}
