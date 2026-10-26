package hub

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// MCP-сервер хаба (Model Context Protocol, транспорт Streamable HTTP,
// POST /api/mcp): ИИ-агенты (Claude Code, Claude Desktop, Cursor…) видят
// nkt как набор инструментов. Только по API-токену, и каждый инструмент —
// внутренний вызов того же API хаба под тем же токеном: роли, пределы по
// хостам и закрытые вызовы — те же, что у токена (tokenPolicy), отдельной
// копии проверок здесь нет. Токен администратора выполняет действия сразу,
// без подтверждения человеком — так задумано для агентов; журнал действий
// пишет их как token:<имя>.

// mcpProtocol — версия протокола, которую сервер знает.
const mcpProtocol = "2025-06-18"

// mcpInternalHeader — пометка внутреннего вызова: «<секрет>:<id токена>».
// Секрет живёт только в памяти процесса: подписанный запрос агента второй
// раз не предъявить (nonce), а внутренний вызов идёт уже проверенным.
const mcpInternalHeader = "X-NKT-MCP-Internal"

var mcpSecret = func() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}()

// mcpInternalToken — токен внутреннего вызова (ok=false — заголовка нет).
func (a *tokenAuth) mcpInternalToken(r *http.Request) (store.APIToken, bool, error) {
	v := r.Header.Get(mcpInternalHeader)
	if v == "" {
		return store.APIToken{}, false, nil
	}
	secret, id, ok := strings.Cut(v, ":")
	if !ok || subtle.ConstantTimeCompare([]byte(secret), []byte(mcpSecret)) != 1 {
		return store.APIToken{}, true, tokenDeny(http.StatusUnauthorized, "auth.tokenInvalid")
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return store.APIToken{}, true, tokenDeny(http.StatusUnauthorized, "auth.tokenInvalid")
	}
	t, err := a.s.db.APITokenByID(r.Context(), n)
	if err != nil {
		return store.APIToken{}, true, tokenDeny(http.StatusUnauthorized, "auth.tokenInvalid")
	}
	return t, true, nil
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

// handleMCP — POST /api/mcp: JSON-RPC 2.0 одним сообщением; уведомления —
// 202 без тела. GET (поток событий от сервера) не поддерживается — 405.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "POST")
		return
	}
	tok, ok := tokenFromContext(r.Context())
	if !ok {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("mcp.tokenOnly"))
		return
	}
	if !s.experimental(r.Context()).MCP {
		writeErr(w, r, http.StatusForbidden, msgs.Errorf("mcp.disabled"))
		return
	}
	var req mcpRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		writeJSON(w, http.StatusOK, mcpResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &mcpError{Code: -32700, Message: "parse error"}})
		return
	}
	if len(req.ID) == 0 { // уведомление (notifications/initialized и т. п.)
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := mcpResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := mcpProtocol
		if p.ProtocolVersion != "" && p.ProtocolVersion <= mcpProtocol {
			version = p.ProtocolVersion
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "nkt-hub", "version": s.hub.Version()},
			"instructions":    mcpInstructions,
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		var tools []map[string]any
		for _, t := range mcpTools {
			if t.allowed(tok) {
				tools = append(tools, t.describe())
			}
		}
		// Операции подключённых плагинов (mcp: true).
		tools = append(tools, s.pluginTools(r.Context(), tok)...)
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &mcpError{Code: -32602, Message: "invalid params"}
			break
		}
		resp.Result = s.mcpCall(r, tok, p.Name, p.Arguments)
	default:
		resp.Error = &mcpError{Code: -32601, Message: "method not found: " + req.Method}
	}
	writeJSON(w, http.StatusOK, resp)
}

const mcpInstructions = `nkt hub: Linux hosts, their findings, services, containers, deployments (pipelines), saved scripts, stack moves between hosts, Traffic Fly (ingress through a VPS gateway) and Kubernetes clusters. Tools act with the API token's role and host scope. Prefer the dry-run variants before changing anything; long operations return a job_id — pass "wait": true to get the outcome and log.`

// mcpText — ответ инструмента текстом.
func mcpText(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}

// mcpCall — инструмент: внутренний вызов API хаба под тем же токеном.
func (s *Server) mcpCall(r *http.Request, tok store.APIToken, name string, args map[string]any) map[string]any {
	var tool *mcpTool
	for i := range mcpTools {
		if mcpTools[i].Name == name {
			tool = &mcpTools[i]
		}
	}
	if tool == nil {
		if out, ok := s.pluginToolCall(r, tok, name, args); ok {
			return out
		}
	}
	if tool == nil || !tool.allowed(tok) {
		return mcpText("unknown or not allowed tool: "+name, true)
	}
	path, body, err := tool.build(args)
	if err != nil {
		return mcpText(err.Error(), true)
	}
	code, out := s.mcpInternal(r, tok, tool.Method, path, body)
	if code >= 400 {
		return mcpText(fmt.Sprintf("HTTP %d: %s", code, out), true)
	}
	if wait, _ := args["wait"].(bool); wait && tool.Job {
		var started struct {
			JobID int64 `json:"job_id"`
		}
		if json.Unmarshal([]byte(out), &started) == nil && started.JobID > 0 {
			return s.mcpWaitJob(r, tok, started.JobID, args)
		}
	}
	return mcpText(out, false)
}

// mcpWaitJob — задание хаба до конца (или до timeout): состояние и хвост
// журнала.
func (s *Server) mcpWaitJob(r *http.Request, tok store.APIToken, id int64, args map[string]any) map[string]any {
	timeout := 600 * time.Second
	if v, ok := args["timeout"].(float64); ok && v > 0 {
		timeout = min(time.Duration(v)*time.Second, time.Hour)
	}
	deadline := time.Now().Add(timeout)
	path := "/hub/jobs/" + strconv.FormatInt(id, 10)
	var job struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	for {
		code, out := s.mcpInternal(r, tok, http.MethodGet, path, nil)
		if code >= 400 {
			return mcpText(fmt.Sprintf("job %d: HTTP %d: %s", id, code, out), true)
		}
		_ = json.Unmarshal([]byte(out), &job)
		if job.Status != store.JobQueued && job.Status != store.JobRunning && job.Status != store.JobInterrupted {
			break
		}
		if time.Now().After(deadline) {
			return mcpText(fmt.Sprintf(`{"job_id":%d,"status":%q,"timed_out":true}`, id, job.Status), false)
		}
		select {
		case <-r.Context().Done():
			return mcpText("cancelled", true)
		case <-time.After(3 * time.Second):
		}
	}
	_, logRaw := s.mcpInternal(r, tok, http.MethodGet, path+"/log?after=0", nil)
	var log struct {
		Lines []struct {
			Text string `json:"text"`
		} `json:"lines"`
	}
	_ = json.Unmarshal([]byte(logRaw), &log)
	lines := make([]string, 0, len(log.Lines))
	for _, l := range log.Lines {
		lines = append(lines, l.Text)
	}
	if len(lines) > 200 {
		lines = append([]string{"…"}, lines[len(lines)-200:]...)
	}
	res, _ := json.Marshal(map[string]any{"job_id": id, "status": job.Status, "error": job.Error, "succeeded": job.Status == store.JobSucceeded, "log": lines})
	return mcpText(string(res), job.Status != store.JobSucceeded)
}

// mcpResultLimit — предел ответа инструмента.
const mcpResultLimit = 200 << 10

// mcpInternal — вызов API хаба под токеном агента: тот же роутер, те же
// проверки прав.
func (s *Server) mcpInternal(r *http.Request, tok store.APIToken, method, path string, body any) (int, string) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	// Чистый контекст: с пользователем из исходного запроса RequireAuth
	// пропустил бы вызов без проверок токена (tokenPolicy).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer context.AfterFunc(r.Context(), cancel)()
	sub, err := http.NewRequestWithContext(ctx, method, "/api"+path, rd)
	if err != nil {
		return http.StatusBadRequest, err.Error()
	}
	// Адрес клиента — тот, что проверен у исходного запроса (список
	// адресов токена).
	sub.RemoteAddr = net.JoinHostPort(tokenClientIP(r), "0")
	sub.Header.Set("Content-Type", "application/json")
	sub.Header.Set(mcpInternalHeader, mcpSecret+":"+strconv.FormatInt(tok.ID, 10))
	sub.Header.Set("X-NKT-Lang", string(msgs.FromContext(r.Context())))
	rec := httptest.NewRecorder()
	s.apiRouter.ServeHTTP(rec, sub)
	out := rec.Body.String()
	if len(out) > mcpResultLimit {
		out = out[:mcpResultLimit] + "\n…(truncated)"
	}
	return rec.Code, out
}

// mcpTool — инструмент: имя, описание, схема входа и вызов API.
type mcpTool struct {
	Name, Description string
	// Method, Path — вызов; {имя} в пути — из аргументов (числа или имена).
	Method, Path string
	// Props — свойства схемы входа: имя → {type, description}; Required.
	Props    map[string]map[string]any
	Required []string
	// Body — какие аргументы уходят телом JSON (пусто — без тела); Query —
	// в строку запроса (GET).
	Body  []string
	Query []string
	// Fixed — поля тела, которые агент не задаёт.
	Fixed map[string]any
	// Admin — инструмент-действие: нужен администратор или токен «по
	// правам» с этим действием (Act, иначе — из таблицы маршрутов); Fly —
	// только токен без предела.
	Admin, Fly bool
	Act        string
	// Job — вызов возвращает job_id: доступен «wait».
	Job bool
	// ReadOnly / Destructive — подсказки агенту (annotations).
	ReadOnly, Destructive bool
}

func (t mcpTool) allowed(tok store.APIToken) bool {
	if t.Fly && tok.Scoped() {
		return false
	}
	if !t.Admin || tok.Role == store.TokenRoleAdmin {
		return true
	}
	act := t.Act
	if act == "" {
		act, _ = tokenRouteAction(t.Method, mcpRoutePattern(t.Path))
	}
	return act != "" && tok.Can(act)
}

var mcpFirstParam = regexp.MustCompile(`\{[a-z_]+\}`)

// mcpRoutePattern — путь инструмента как шаблон маршрута: первый
// параметр — {id} («/hub/pipelines/{pipeline_id}/deploy» →
// «/hub/pipelines/{id}/deploy»).
func mcpRoutePattern(path string) string {
	done := false
	return mcpFirstParam.ReplaceAllStringFunc(path, func(m string) string {
		if done {
			return m
		}
		done = true
		return "{id}"
	})
}

func (t mcpTool) describe() map[string]any {
	props := map[string]any{}
	for k, v := range t.Props {
		props[k] = v
	}
	if t.Job {
		props["wait"] = map[string]any{"type": "boolean", "description": "Wait for the hub job to finish and return its status and log"}
		props["timeout"] = map[string]any{"type": "number", "description": "Seconds to wait (default 600, max 3600)"}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(t.Required) > 0 {
		schema["required"] = t.Required
	}
	return map[string]any{
		"name": t.Name, "description": t.Description, "inputSchema": schema,
		"annotations": map[string]any{"readOnlyHint": t.ReadOnly, "destructiveHint": t.Destructive, "openWorldHint": false},
	}
}

var mcpPathArg = regexp.MustCompile(`\{([a-z_]+)\}`)
var mcpSafeSegment = regexp.MustCompile(`^-?[A-Za-z0-9._-]{1,128}$`)

// build — путь (с подставленными аргументами) и тело.
func (t mcpTool) build(args map[string]any) (string, any, error) {
	var bad error
	path := mcpPathArg.ReplaceAllStringFunc(t.Path, func(m string) string {
		name := m[1 : len(m)-1]
		v := fmt.Sprint(args[name])
		if f, ok := args[name].(float64); ok {
			v = strconv.FormatInt(int64(f), 10)
		}
		if args[name] == nil || !mcpSafeSegment.MatchString(v) {
			bad = fmt.Errorf("argument %s: missing or not a plain value", name)
		}
		return v
	})
	if bad != nil {
		return "", nil, bad
	}
	// Машина хаба (-1) — свой путь /hosts/local/….
	if rest, ok := strings.CutPrefix(path, "/hosts/"+strconv.Itoa(localHostID)+"/"); ok {
		path = "/hosts/local/" + rest
	}
	if q, ok := args["after"].(float64); ok && t.Method == http.MethodGet {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "after=" + strconv.FormatInt(int64(q), 10)
	}
	for _, k := range t.Query {
		if v, ok := args[k].(string); ok && v != "" {
			if !mcpSafeSegment.MatchString(v) {
				return "", nil, fmt.Errorf("argument %s: not a plain value", k)
			}
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			path += sep + k + "=" + v
		}
	}
	if len(t.Body) == 0 {
		return path, nil, nil
	}
	body := map[string]any{}
	for k, v := range t.Fixed {
		body[k] = v
	}
	for _, k := range t.Body {
		if v, ok := args[k]; ok {
			body[k] = v
		}
	}
	return path, body, nil
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

// mcpTools — инструменты: то, что открыто API-токену (apitokens.go).
var mcpTools = []mcpTool{
	{Name: "list_hosts", Description: "Hosts in the token's scope: status, nkt version, problem counts.", Method: "GET", Path: "/hub/hosts", ReadOnly: true},
	{Name: "host_overview", Description: "Overview of a host: OS, uptime, counts of findings, listeners, containers.", Method: "GET", Path: "/hosts/{host_id}/overview",
		Props: map[string]map[string]any{"host_id": prop("integer", "Host id from list_hosts; the hub machine is -1")}, Required: []string{"host_id"}, ReadOnly: true},
	{Name: "host_findings", Description: "Findings (configuration problems) of a host with severity, detail and suggestion.", Method: "GET", Path: "/hosts/{host_id}/findings",
		Props: map[string]map[string]any{"host_id": prop("integer", "Host id")}, Required: []string{"host_id"}, ReadOnly: true},
	// Хосты плагинов (kind "plugin" в list_hosts: MikroTik, Windows…) — без
	// агента nkt: их разделы — через плагин.
	{Name: "device_snapshot", Description: "Inventory of a plugin host (kind \"plugin\" in list_hosts — MikroTik, Windows…): info, interfaces, services, packages, ports, users, neighbors, extra tables.", Method: "GET", Path: "/hub/plugin-hosts/{host_id}/snapshot",
		Props: map[string]map[string]any{"host_id": prop("integer", "Plugin host id from list_hosts")}, Required: []string{"host_id"}, ReadOnly: true},
	{Name: "device_findings", Description: "Findings of a plugin host (severity critical | warning | info, title, detail, fix).", Method: "GET", Path: "/hub/plugin-hosts/{host_id}/findings",
		Props: map[string]map[string]any{"host_id": prop("integer", "Plugin host id")}, Required: []string{"host_id"}, ReadOnly: true},
	{Name: "device_config", Description: "Configuration versions of a plugin host (newest first; the hub keeps them, fresh=true reads the device now) — compare to see what changed.", Method: "GET", Path: "/hub/plugin-hosts/{host_id}/config",
		Props: map[string]map[string]any{"host_id": prop("integer", "Plugin host id"), "fresh": prop("boolean", "Read the device now")}, Required: []string{"host_id"}, Query: []string{"fresh"}, ReadOnly: true},
	{Name: "host_services", Description: "systemd services of a host.", Method: "GET", Path: "/hosts/{host_id}/services",
		Props: map[string]map[string]any{"host_id": prop("integer", "Host id")}, Required: []string{"host_id"}, ReadOnly: true},
	{Name: "host_containers", Description: "Docker containers of a host.", Method: "GET", Path: "/hosts/{host_id}/containers",
		Props: map[string]map[string]any{"host_id": prop("integer", "Host id")}, Required: []string{"host_id"}, ReadOnly: true},
	{Name: "list_alerts", Description: "Hub alerts newer than an id.", Method: "GET", Path: "/hub/events",
		Props: map[string]map[string]any{"after": prop("integer", "Return alerts with id greater than this (0 — recent)")}, ReadOnly: true},
	{Name: "list_pipelines", Description: "Deployment pipelines with their last deployment.", Method: "GET", Path: "/hub/pipelines", ReadOnly: true},
	{Name: "get_pipeline", Description: "A pipeline with its description (YAML; secrets are @secret).", Method: "GET", Path: "/hub/pipelines/{pipeline_id}",
		Props: map[string]map[string]any{"pipeline_id": prop("integer", "Pipeline id")}, Required: []string{"pipeline_id"}, ReadOnly: true},
	{Name: "list_pipeline_examples", Description: "Ready pipeline descriptions (Forgejo, n8n, WordPress, Technitium DNS, a Forgejo Actions runner…): without key — the list; with key — the description to adapt for create_pipeline (hosts, domains, env).", Method: "GET", Path: "/hub/pipelines/examples",
		Props: map[string]map[string]any{"key": prop("string", "Example key from the list")}, Query: []string{"key"}, ReadOnly: true},
	{Name: "job_status", Description: "A hub job: status (queued, running, succeeded, failed) and error.", Method: "GET", Path: "/hub/jobs/{job_id}",
		Props: map[string]map[string]any{"job_id": prop("integer", "Job id")}, Required: []string{"job_id"}, ReadOnly: true},
	{Name: "job_log", Description: "Log lines of a hub job.", Method: "GET", Path: "/hub/jobs/{job_id}/log",
		Props: map[string]map[string]any{"job_id": prop("integer", "Job id"), "after": prop("integer", "Line number to continue after (0 — from the start)")}, Required: []string{"job_id"}, ReadOnly: true},
	{Name: "list_scripts", Description: "Saved hub scripts.", Method: "GET", Path: "/hub/scripts", ReadOnly: true},
	{Name: "get_script", Description: "Text of a saved hub script.", Method: "GET", Path: "/hub/scripts/{script_id}",
		Props: map[string]map[string]any{"script_id": prop("integer", "Script id")}, Required: []string{"script_id"}, ReadOnly: true},
	{Name: "list_migrations", Description: "Record of stack moves between hosts.", Method: "GET", Path: "/hub/migrations", ReadOnly: true},
	{Name: "list_clusters", Description: "Kubernetes clusters and their nodes.", Method: "GET", Path: "/hub/clusters", ReadOnly: true},
	{Name: "fly_map", Description: "Traffic Fly map: routes, targets, gateways.", Method: "GET", Path: "/hub/fly", ReadOnly: true, Fly: true},
	{Name: "fly_status", Description: "Traffic Fly status: target health, tunnels, certificates.", Method: "GET", Path: "/hub/fly/status", ReadOnly: true, Fly: true},

	{Name: "check_script", Description: "Parse and check a script text against the hub (steps, errors, questions asked at start). Changes nothing.", Method: "POST", Path: "/hub/scripts/check",
		Props: map[string]map[string]any{"content": prop("string", "Script text")}, Required: []string{"content"}, Body: []string{"content"}, Admin: true, ReadOnly: true},
	{Name: "run_script", Description: "Run a saved hub script. dry_run=true only asks the hosts for plans; dry_first=true does a dry run first and runs only if it passed (one job). values — answers to the start questions, e.g. {\"param:FROM\":\"web1\"}.", Method: "POST", Path: "/hub/scripts/{script_id}/run",
		Props: map[string]map[string]any{"script_id": prop("integer", "Script id"), "dry_run": prop("boolean", "Only plan, change nothing"),
			"dry_first": prop("boolean", "Dry run first, then the run if it passed"), "values": prop("object", "Answers to the script's questions")},
		Required: []string{"script_id"}, Body: []string{"dry_run", "dry_first", "values"}, Admin: true, Job: true, Destructive: true},
	{Name: "deploy_pipeline", Description: "Deploy a pipeline (optionally a ref or tag).", Method: "POST", Path: "/hub/pipelines/{pipeline_id}/deploy",
		Props:    map[string]map[string]any{"pipeline_id": prop("integer", "Pipeline id"), "ref": prop("string", "Branch (empty — the pipeline's)"), "tag": prop("string", "Tag (empty — branch head)")},
		Required: []string{"pipeline_id"}, Body: []string{"ref", "tag"}, Admin: true, Job: true, Destructive: true},
	{Name: "pipeline_power", Description: "Start or stop what a compose pipeline has deployed, on all its hosts: stop keeps containers and data; start brings up the same version (images from the host, the pipeline's registry key only for missing ones). Alerts for a stopped deployment's Traffic Fly targets still come, marked as stopped.", Method: "POST", Path: "/hub/pipelines/{pipeline_id}/{action}",
		Props:    map[string]map[string]any{"pipeline_id": prop("integer", "Pipeline id"), "action": prop("string", "start | stop")},
		Required: []string{"pipeline_id", "action"}, Admin: true, Act: store.TokenActPipelinePower, Job: true, Destructive: true},
	{Name: "dry_run_pipeline", Description: "Dry run of a compose pipeline: checks hosts, images, ports, site; changes nothing. A saved one (pipeline_id) or a description text not saved yet (content; with pipeline_id — its secrets are used).", Method: "POST", Path: "/hub/pipelines/dryrun",
		Props: map[string]map[string]any{"pipeline_id": prop("integer", "Pipeline id"), "content": prop("string", "Pipeline description (YAML) to check instead of the saved one")},
		Body:  []string{"pipeline_id", "content"}, Admin: true, Job: true, ReadOnly: true},
	{Name: "create_pipeline", Description: "Create a pipeline from a description (YAML, as in the editor; start from list_pipeline_examples). Values of the env: block move to the pipeline's encrypted .env, the description keeps @secret; generate: values are created by the hub. Doesn't deploy — then dry_run_pipeline and deploy_pipeline. A limited token: compose only, on its hosts; the pipeline becomes its own.", Method: "POST", Path: "/hub/pipelines",
		Props:    map[string]map[string]any{"name": prop("string", "Pipeline name"), "content": prop("string", "Pipeline description (YAML)")},
		Required: []string{"name", "content"}, Body: []string{"name", "content"}, Admin: true},
	{Name: "update_pipeline", Description: "Replace a pipeline's description (a new version in its history; the answer has the diff). An env: value that would replace one already set fails with env_conflict unless replace_env=true. A limited token: only its own pipelines.", Method: "PUT", Path: "/hub/pipelines/{pipeline_id}",
		Props: map[string]map[string]any{"pipeline_id": prop("integer", "Pipeline id"), "content": prop("string", "The whole new description"), "note": prop("string", "Version note"),
			"replace_env": prop("boolean", "Replace .env values already set")},
		Required: []string{"pipeline_id", "content"}, Body: []string{"content", "note", "replace_env"}, Admin: true, Destructive: true},
	{Name: "remove_pipeline", Description: "Remove a compose pipeline: a job takes its stack down on all hosts (stacks shared with others stay) and deletes the pipeline. volumes=true also deletes volumes and the stack directory (otherwise kept aside), images — its images, cert — the site certificate. A limited token: only its own pipelines.", Method: "POST", Path: "/hub/pipelines/{pipeline_id}/remove",
		Props: map[string]map[string]any{"pipeline_id": prop("integer", "Pipeline id"), "volumes": prop("boolean", "Also delete volumes and data"),
			"images": prop("boolean", "Also delete images"), "cert": prop("boolean", "Also delete the site certificate")},
		Required: []string{"pipeline_id"}, Body: []string{"volumes", "images", "cert"}, Fixed: map[string]any{"all": true}, Admin: true, Job: true, Destructive: true},
	{Name: "migrate_preview", Description: "Plan of moving a container's stack to another host: what travels, obstacles, Traffic Fly routes, pipeline.", Method: "POST", Path: "/hub/migrate/preview",
		Props:    map[string]map[string]any{"from": prop("integer", "Host id the stack runs on"), "to": prop("integer", "Target host id"), "container": prop("string", "A container of the stack")},
		Required: []string{"from", "to", "container"}, Body: []string{"from", "to", "container"}, Admin: true, ReadOnly: true},
	{Name: "migrate_stack", Description: "Move a container's stack to another host (stops it during the data copy; rolls back on error).", Method: "POST", Path: "/hub/migrate",
		Props: map[string]map[string]any{"from": prop("integer", "Host id"), "to": prop("integer", "Target host id"), "container": prop("string", "A container of the stack"),
			"switch_fly": prop("boolean", "Switch Traffic Fly targets to the new host"), "switch_pipeline": prop("boolean", "Switch the stack's pipeline to the new host"), "clean_leftovers": prop("boolean", "Remove leftovers of an earlier copy on the new host")},
		Required: []string{"from", "to", "container"}, Body: []string{"from", "to", "container", "switch_fly", "switch_pipeline", "clean_leftovers"}, Admin: true, Job: true, Destructive: true},
	{Name: "migration_action", Description: "After a move: rollback, remove (the stopped old copy; data=true also its volumes) or stopnew (stop the second copy).", Method: "POST", Path: "/hub/migrations/{migration_id}/{action}",
		Props: map[string]map[string]any{"migration_id": prop("string", "Move record id from list_migrations"), "action": prop("string", "rollback | remove | stopnew"), "data": prop("boolean", "remove: also the volumes"),
			"skip_new": prop("boolean", "rollback: the new host does not answer — do not stop the copy there")},
		Required: []string{"migration_id", "action"}, Body: []string{"data", "skip_new"}, Admin: true, Job: true, Destructive: true},
	{Name: "fly_apply", Description: "Apply the Traffic Fly map to gateways and hosts (dry_run=true only checks).", Method: "POST", Path: "/hub/fly/apply",
		Props: map[string]map[string]any{"dry_run": prop("boolean", "Only check")}, Body: []string{"dry_run"}, Admin: true, Fly: true, Job: true, Destructive: true},
	{Name: "fly_ops", Description: "Point changes to Traffic Fly route targets: [{op: add|remove|active|reserve|drain, route, target?, host_id?, kind?, ref?, port?}].", Method: "POST", Path: "/hub/fly/ops",
		Props: map[string]map[string]any{"ops": prop("array", "Operations"), "note": prop("string", "Version note")}, Required: []string{"ops"}, Body: []string{"ops", "note"}, Admin: true, Fly: true, Job: true, Destructive: true},
	{Name: "manifest_diff", Description: "kubectl diff of a manifest against clusters.", Method: "POST", Path: "/hub/k8s/manifests/diff",
		Props:    map[string]map[string]any{"name": prop("string", "Manifest name"), "content": prop("string", "YAML"), "clusters": prop("array", "Cluster ids")},
		Required: []string{"name", "content", "clusters"}, Body: []string{"name", "content", "clusters"}, Admin: true, ReadOnly: true},
	{Name: "manifest_apply", Description: "kubectl apply of a manifest to clusters (saved on the hub with the result).", Method: "POST", Path: "/hub/k8s/manifests/apply?job=1",
		Props:    map[string]map[string]any{"name": prop("string", "Manifest name"), "content": prop("string", "YAML"), "clusters": prop("array", "Cluster ids"), "note": prop("string", "Note")},
		Required: []string{"name", "content", "clusters"}, Body: []string{"name", "content", "clusters", "note"}, Admin: true, Job: true, Destructive: true},
	{Name: "device_action", Description: "An action of a plugin host's type (see its type's actions: reboot, update, backup…); dry_run=true only says what it would do.", Method: "POST", Path: "/hub/plugin-hosts/{host_id}/action",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Plugin host id"), "action": prop("string", "Action name"), "args": prop("object", "Action fields"), "dry_run": prop("boolean", "Only say what would be done")},
		Required: []string{"host_id", "action"}, Body: []string{"action", "args", "dry_run"}, Admin: true, Destructive: true},
	{Name: "device_exec", Description: "Run a command on a plugin host in its type's language (RouterOS script, PowerShell); dry_run=true only says what it would run.", Method: "POST", Path: "/hub/plugin-hosts/{host_id}/exec",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Plugin host id"), "command": prop("string", "Command text"), "dry_run": prop("boolean", "Only say what would be run")},
		Required: []string{"host_id", "command"}, Body: []string{"command", "dry_run"}, Admin: true, Destructive: true},
	{Name: "service_action", Description: "start, stop, restart or reload a systemd service on a host.", Method: "POST", Path: "/hosts/{host_id}/services/{name}/{action}",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Host id"), "name": prop("string", "Service name"), "action": prop("string", "start | stop | restart | reload")},
		Required: []string{"host_id", "name", "action"}, Admin: true, Destructive: true},
	{Name: "container_action", Description: "start, stop or restart a Docker container on a host.", Method: "POST", Path: "/hosts/{host_id}/containers/{name}/{action}",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Host id"), "name": prop("string", "Container name"), "action": prop("string", "start | stop | restart")},
		Required: []string{"host_id", "name", "action"}, Admin: true, Destructive: true},
	{Name: "create_vm", Description: "Create a virtual machine on a host from a cloud image (like the “New machine” window). A limited token: within its vCPU, memory, disk and machine-count limits; the machine is the token's own and lives in the token's own isolated NAT network (network and bridge cannot be set).", Method: "POST", Path: "/hub/vm/provision",
		Props: map[string]map[string]any{"host_id": prop("integer", "Host to create on"), "spec": prop("object", "name, image_id, vcpus, memory_mb, disk_gb, user, ssh_key (optional: network, packages, autostart, user_data — own #cloud-config)"),
			"install_nkt": prop("boolean", "Install nkt inside")},
		Required: []string{"host_id", "spec"}, Body: []string{"host_id", "spec", "install_nkt"}, Admin: true, Job: true, Destructive: true},
	{Name: "vm_power", Description: "start, shutdown, destroy (force off) or reboot a machine; host_id is the machine's own id from list_hosts. A limited token: only its own machines.", Method: "POST", Path: "/hub/hosts/{host_id}/vm/{action}",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id"), "action": prop("string", "start | shutdown | destroy | reboot")},
		Required: []string{"host_id", "action"}, Admin: true, Destructive: true},
	{Name: "vm_snapshots", Description: "List a machine's snapshots (oldest first); host_id is the machine's own id.", Method: "GET", Path: "/hub/hosts/{host_id}/vm/snapshots",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id")},
		Required: []string{"host_id"}, Admin: true, ReadOnly: true},
	{Name: "vm_snapshot", Description: "Take a snapshot of a machine (with memory if it runs). A limited token: up to its snapshots-per-machine limit (0 — none).", Method: "POST", Path: "/hub/hosts/{host_id}/vm/snapshots",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id"), "name": prop("string", "Snapshot name: Latin letters, digits, . _ -")},
		Required: []string{"host_id", "name"}, Body: []string{"name"}, Admin: true},
	{Name: "vm_snapshot_revert", Description: "Revert a machine to a snapshot (changes after it are lost).", Method: "POST", Path: "/hub/hosts/{host_id}/vm/snapshots/{name}/revert",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id"), "name": prop("string", "Snapshot name")},
		Required: []string{"host_id", "name"}, Admin: true, Destructive: true},
	{Name: "vm_snapshot_delete", Description: "Delete a machine's snapshot.", Method: "DELETE", Path: "/hub/hosts/{host_id}/vm/snapshots/{name}",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id"), "name": prop("string", "Snapshot name")},
		Required: []string{"host_id", "name"}, Admin: true, Destructive: true},
	{Name: "vm_resize", Description: "Change a machine's vCPU, memory (MB) and disk (GB, grows only); 0 or absent — keep. vCPU and memory apply after shutdown and start (restart_needed). A limited token: within its limits.", Method: "POST", Path: "/hub/hosts/{host_id}/vm/resize",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id"), "vcpus": prop("integer", "New vCPU count"), "memory_mb": prop("integer", "New memory, MB"), "disk_gb": prop("integer", "New disk size, GB")},
		Required: []string{"host_id"}, Body: []string{"vcpus", "memory_mb", "disk_gb"}, Admin: true},
	{Name: "vm_reinstall", Description: "Reinstall a machine from an image: a new disk and first-boot settings, the same name, network and size; snapshots and data are lost. user_data — own #cloud-config (packages, write_files, runcmd; not users or keys). A limited token: catalog images only.", Method: "POST", Path: "/hub/hosts/{host_id}/vm/reinstall",
		Props: map[string]map[string]any{"host_id": prop("integer", "Machine id"), "image_id": prop("string", "Image id from the catalog"), "ssh_key": prop("string", "Public SSH key (optional)"),
			"user_data": prop("string", "Own #cloud-config (optional, up to 64 KB)"), "install_nkt": prop("boolean", "Install nkt again")},
		Required: []string{"host_id", "image_id"}, Body: []string{"image_id", "ssh_key", "user_data", "install_nkt"}, Admin: true, Job: true, Destructive: true},
	{Name: "vm_ports", Description: "Ports of the host's pool open to a machine: host port → machine port (tcp/udp) and the host address.", Method: "GET", Path: "/hub/hosts/{host_id}/vm/ports",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id")},
		Required: []string{"host_id"}, Admin: true, ReadOnly: true},
	{Name: "vm_port_open", Description: "Open a free port of the host's pool to a machine port (the machine's address gets pinned). A limited token: its own machines, up to its ports limit.", Method: "POST", Path: "/hub/hosts/{host_id}/vm/ports",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id"), "vm_port": prop("integer", "Port inside the machine"), "proto": prop("string", "tcp (default) | udp")},
		Required: []string{"host_id", "vm_port"}, Body: []string{"vm_port", "proto"}, Admin: true},
	{Name: "vm_port_close", Description: "Close a host port open to a machine.", Method: "DELETE", Path: "/hub/hosts/{host_id}/vm/ports/{port}",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id"), "port": prop("integer", "Host port")},
		Required: []string{"host_id", "port"}, Admin: true, Destructive: true},
	{Name: "fly_own_routes", Description: "A token's own Traffic Fly routes (HTTPS by name in its domain to its machines) and the domain.", Method: "GET", Path: "/hub/own/fly", Admin: true, ReadOnly: true},
	{Name: "fly_own_add", Description: "Publish an own machine by name over HTTPS (certificate on the gateway): the name must be the token's domain or under it.", Method: "POST", Path: "/hub/own/fly",
		Props:    map[string]map[string]any{"name": prop("string", "Name, e.g. app.client.example.com"), "host_id": prop("integer", "Machine id"), "port": prop("integer", "HTTP port inside the machine")},
		Required: []string{"name", "host_id", "port"}, Body: []string{"name", "host_id", "port"}, Admin: true, Job: true},
	{Name: "fly_own_remove", Description: "Remove an own Traffic Fly route.", Method: "DELETE", Path: "/hub/own/fly/{route}",
		Props:    map[string]map[string]any{"route": prop("string", "Route id from fly_own_routes")},
		Required: []string{"route"}, Admin: true, Job: true, Destructive: true},
	{Name: "vm_delete", Description: "Delete a machine entirely: power off, the domain with its disks, its host record and the token's usage.", Method: "DELETE", Path: "/hub/hosts/{host_id}/vm",
		Props:    map[string]map[string]any{"host_id": prop("integer", "Machine id")},
		Required: []string{"host_id"}, Admin: true, Job: true, Destructive: true},
	{Name: "fail2ban_ban", Description: "Ban (action=ban) or unban (action=unban) IP addresses on hosts with fail2ban.", Method: "POST", Path: "/hub/fail2ban/fleet",
		Props:    map[string]map[string]any{"action": prop("string", "ban | unban"), "ips": prop("array", "IP addresses"), "host_ids": prop("array", "Host ids (empty — all with fail2ban)")},
		Required: []string{"action", "ips"}, Body: []string{"action", "ips", "host_ids"}, Admin: true, Job: true, Destructive: true},
}
