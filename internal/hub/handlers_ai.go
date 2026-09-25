package hub

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/ai"
	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/model"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
)

// API разбора моделью. Всё идёт через хаб: настройка одна на установку,
// ключ лежит только здесь, хостам наружу ничего не нужно.

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.hub.AIStatusFor(r.Context()))
}

type aiSettingsRequest struct {
	Enabled    bool   `json:"enabled"`
	Provider   string `json:"provider"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	Anonymize  bool   `json:"anonymize"`
	DailyLimit int    `json:"daily_limit"`
	TimeoutS   int    `json:"timeout_s"`
	// APIKey: null — оставить прежний, "" — стереть, иначе заменить.
	APIKey *string `json:"api_key"`
}

func (s *Server) handleAISettings(w http.ResponseWriter, r *http.Request) {
	var req aiSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	set := ai.Settings{
		Enabled: req.Enabled, Provider: req.Provider, BaseURL: strings.TrimSpace(req.BaseURL),
		Model: req.Model, Anonymize: req.Anonymize, DailyLimit: req.DailyLimit, TimeoutS: req.TimeoutS,
	}
	if err := s.hub.SetAISettings(r.Context(), set, req.APIKey); err != nil {
		fail(w, r, err)
		return
	}
	// Ключ в журнал не попадает — только факт изменения настроек.
	s.db.Audit(r.Context(), auth.Username(r.Context()), "ai.settings", ai.Describe(set), "ok", nil)
	writeJSON(w, http.StatusOK, s.hub.AIStatusFor(r.Context()))
}

// handleAITest — «проверить»: живой запрос к модели с теми настройками,
// что сейчас в форме. Без этого ошибка настройки обнаруживается только
// при первом разборе и выглядит как «сломался ИИ», а не «ключ не тот».
func (s *Server) handleAITest(w http.ResponseWriter, r *http.Request) {
	var req aiSettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	set := ai.Settings{
		Provider: req.Provider, BaseURL: strings.TrimSpace(req.BaseURL), Model: strings.TrimSpace(req.Model),
		Anonymize: req.Anonymize, DailyLimit: req.DailyLimit, TimeoutS: req.TimeoutS,
	}
	s.aiExtendDeadline(w, normalizeAITimeout(req.TimeoutS))
	res, err := s.hub.AITest(r.Context(), set, req.APIKey)
	if err != nil {
		fail(w, r, err)
		return
	}
	// Запрос ушёл наружу — в журнале должно остаться, кто и куда.
	outcome := "ok"
	if !res.OK {
		outcome = "error"
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "ai.test", ai.Describe(set), outcome, nil)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleAICacheClear(w http.ResponseWriter, r *http.Request) {
	if err := s.db.AICacheClear(r.Context()); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "ai.cache_clear", "", "ok", nil)
	writeJSON(w, http.StatusOK, s.hub.AIStatusFor(r.Context()))
}

// aiExtendDeadline отодвигает срок записи ответа: у сервера общий
// WriteTimeout в две минуты (cmd/nkt/main.go), а модель вправе думать
// столько, сколько разрешено в настройках, — иначе ответ, который она
// всё-таки дала, оборвётся на полпути к браузеру.
func (s *Server) aiExtendDeadline(w http.ResponseWriter, timeoutS int) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Duration(timeoutS)*time.Second + 30*time.Second))
}

// aiExplainRequest — одна находка на разбор. Поля повторяют то, что
// интерфейс уже показывает в строке: он и отправляет их как есть.
type aiExplainRequest struct {
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Suggestion string `json:"suggestion"`
	Severity   string `json:"severity"`
	Service    string `json:"service"`
	Object     string `json:"object"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	// Diff и Output — для ошибки правки конфигурации: что меняли и что
	// ответила проверка.
	Diff   string `json:"diff"`
	Output string `json:"output"`
	// Content и Question — помощь по конфигурации: текст файла и вопрос.
	Content  string `json:"content"`
	Question string `json:"question"`
	// HostID — на каком хосте найдено (0 — сам хаб/localhost): по нему
	// собирается контекст «что рядом».
	HostID int64 `json:"host_id"`
	// Force — спросить модель заново, минуя сохранённый и «похожий» ответ.
	Force bool `json:"force"`
}

func (s *Server) handleAIExplain(w http.ResponseWriter, r *http.Request) {
	var req aiExplainRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ai.badResponse", "empty title"))
		return
	}
	kind := req.Kind
	switch kind {
	case ai.KindFinding, ai.KindVuln, ai.KindMalware, ai.KindEvent, ai.KindJobError, ai.KindConfigError, ai.KindConfig:
	default:
		kind = ai.KindFinding
	}
	fc := ai.FindingContext{
		Kind: kind, Title: req.Title, Detail: req.Detail, Suggestion: req.Suggestion,
		Severity: req.Severity, Service: req.Service, Object: req.Object, File: req.File, Line: req.Line,
		Diff: req.Diff, Output: req.Output, Content: req.Content, Question: req.Question,
	}
	fc.Host, fc.Around = s.aiHostContext(r.Context(), req.HostID)
	s.aiExtendDeadline(w, s.hub.AISettings(r.Context()).TimeoutS)
	answer, err := s.hub.AIExplain(r.Context(), kind, fc, s.aiKnownNames(r.Context()), req.HostID, req.Force)
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

// handleAIAnswers — ссылки на все сохранённые ответы: страница красит по
// ним лампочки (свой ответ есть / такая находка разбиралась на другом
// хосте).
func (s *Server) handleAIAnswers(w http.ResponseWriter, r *http.Request) {
	refs, err := s.db.AIAnswerRefs(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	if refs == nil {
		refs = []store.AIAnswerRef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"answers": refs})
}

// handleAIAnswerDelete — «удалить ответ» в окне разбора.
func (s *Server) handleAIAnswerDelete(w http.ResponseWriter, r *http.Request) {
	var req aiExplainRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.hub.AIAnswerDelete(r.Context(), req.Kind, req.Title, req.Object, req.File, req.HostID); err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type aiPromptRequest struct {
	Kind string `json:"kind"`
	Lang string `json:"lang"`
	Text string `json:"text"`
}

func (s *Server) handleAIPrompts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prompts": s.hub.AIPrompts(r.Context())})
}

// handleAIPromptSet — сохранить правленую инструкцию (пустой текст —
// вернуть стандартную).
func (s *Server) handleAIPromptSet(w http.ResponseWriter, r *http.Request) {
	var req aiPromptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.hub.SetAIPrompt(r.Context(), req.Kind, req.Lang, req.Text); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "ai.prompt", req.Kind+"/"+req.Lang, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"prompts": s.hub.AIPrompts(r.Context())})
}

// handleAIPromptDiff — отличия черновика от стандартной инструкции: окно
// подтверждения показывает их до сохранения.
func (s *Server) handleAIPromptDiff(w http.ResponseWriter, r *http.Request) {
	var req aiPromptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	diff := s.hub.AIPromptDiff(r.Context(), req.Kind, req.Lang, req.Text)
	writeJSON(w, http.StatusOK, map[string]any{"diff": diff, "changed": diff != ""})
}

// aiHostContext — короткая справка о хосте и о том, что на нём рядом.
// Без неё модель отвечает общими словами: «чем грозит здесь» требует
// знать, публичен ли порт и что за сервис его слушает.
func (s *Server) aiHostContext(ctx context.Context, hostID int64) (string, []string) {
	snap := s.localSnapshot()
	name := "localhost"
	if hostID > 0 {
		if h, err := s.db.HostByID(ctx, hostID); err == nil {
			name = h.Name
			if ov, ok := s.hub.Overview(hostID); ok {
				snap = nil
				return fmt.Sprintf("%s (%s)", name, hostSummary(ov)), nil
			}
		}
	}
	if snap == nil {
		return name, nil
	}
	head := fmt.Sprintf("%s: %s, %s", name, snap.Host.OS, snap.Host.Kernel)
	var around []string
	for _, l := range snap.Listeners {
		if len(around) >= 12 {
			break
		}
		access := "локальный"
		if l.Public() {
			access = "публичный"
		}
		around = append(around, fmt.Sprintf("порт %d/%s %s, процесс %s", l.Port, l.Protocol, access, l.Process))
	}
	for _, c := range snap.Container {
		if len(around) >= 20 {
			break
		}
		around = append(around, fmt.Sprintf("контейнер %s (%s), запущен: %t", c.Name, c.Image, c.Running))
	}
	if len(snap.Firewall.Managers) > 0 {
		var fw []string
		for _, mgr := range snap.Firewall.Managers {
			if mgr.Installed {
				state := "выключен"
				if mgr.Active {
					state = "включён"
				}
				fw = append(fw, fmt.Sprintf("%s: %s %s", mgr.Name, state, mgr.Policy))
			}
		}
		if len(fw) > 0 {
			around = append(around, "firewall — "+strings.Join(fw, ", "))
		}
	}
	return head, around
}

func hostSummary(ov HostOverview) string {
	return fmt.Sprintf("проблем: critical %d, high %d", ov.Findings["critical"], ov.Findings["high"])
}

// localSnapshot — снимок собственной машины хаба, если он есть.
func (s *Server) localSnapshot() *model.Snapshot {
	if s.localScanner == nil {
		return nil
	}
	return s.localScanner.Latest()
}

// aiKnownNames — имена хостов и машин: они прячутся псевдонимами, если
// включена анонимизация.
func (s *Server) aiKnownNames(ctx context.Context) []string {
	hosts, err := s.db.ListHosts(ctx)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(hosts))
	for _, h := range hosts {
		names = append(names, h.Name)
	}
	return names
}

// handleAIReview — архитектурный разбор карты ресурсов. scope: «hub» —
// все хосты сразу, иначе id хоста.
func (s *Server) handleAIReview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope  string   `json:"scope"`
		HostID int64    `json:"host_id"`
		Lines  []string `json:"lines"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	scope := "hub"
	if req.Scope != "hub" {
		if req.HostID == 0 {
			writeErr(w, r, http.StatusBadRequest, msgs.Errorf("hub.invalidHostId"))
			return
		}
		scope = "host:" + strconv.FormatInt(req.HostID, 10)
	}
	lines := req.Lines
	if scope == "hub" {
		lines = append(lines, s.aiHubLines(r.Context())...)
	}
	if len(lines) == 0 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("ai.badResponse", "empty map"))
		return
	}
	s.aiExtendDeadline(w, s.hub.AISettings(r.Context()).TimeoutS)
	answer, err := s.hub.AIReviewMap(r.Context(), scope, lines, s.aiKnownNames(r.Context()), auth.Username(r.Context()))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

// aiHubLines — карта уровня хаба: хосты, их роли и состояние. Строится
// из того, что хаб знает сам, без обращения к хостам.
func (s *Server) aiHubLines(ctx context.Context) []string {
	hosts, err := s.db.ListHosts(ctx)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(hosts)+1)
	out = append(out, "Хосты хаба:")
	for _, h := range hosts {
		line := fmt.Sprintf("- %s (%s), статус %s", h.Name, h.Addr, h.Status)
		if h.Group != "" {
			line += ", группа " + h.Group
		}
		if h.ClusterID != 0 {
			line += fmt.Sprintf(", узел кластера %d, роль %s", h.ClusterID, h.K8sRole)
		}
		if h.ParentID != 0 {
			line += ", машина на другом хосте"
		}
		if ov, ok := s.hub.Overview(h.ID); ok {
			line += fmt.Sprintf(", проблем critical %d / high %d", ov.Findings["critical"], ov.Findings["high"])
		}
		out = append(out, line)
	}
	return out
}

// handleAIReviews — история разборов для области.
func (s *Server) handleAIReviews(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "hub"
	}
	list, err := s.db.AIReviews(r.Context(), scope, intQuery(r, "limit", 10))
	if err != nil {
		fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": list})
}

func intQuery(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
