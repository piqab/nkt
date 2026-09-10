package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/jobs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
	"github.com/piqab/nkt/internal/vmimage"
)

// Каталог облачных образов и создание машины из образа. Обе операции
// долгие (сотни мегабайт и копирование диска), поэтому обе идут фоновым
// заданием — браузер для них не нужен.

func (s *Server) handleVMImages(w http.ResponseWriter, r *http.Request) {
	if s.vmimages == nil {
		writeError(w, http.StatusServiceUnavailable, "работа с образами недоступна")
		return
	}
	// Заодно отвечаем, чем на этом хосте машины вообще создавать: без
	// qemu-img и virsh форма создания только обманывала бы ожидания.
	tools := vmcreate.CheckTools(r.Context(), s.scanner.Collector())
	writeJSON(w, http.StatusOK, map[string]any{
		"catalog": vmimage.Catalog,
		"local":   s.vmimages.Status(),
		"dir":     s.vmimages.Dir(),
		"tools":   tools,
		"missing": vmcreate.MissingTools(tools),
	})
}

// handleVMToolsInstall доставляет пакеты, без которых машину не создать.
func (s *Server) handleVMToolsInstall(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "фоновые задания недоступны")
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind:   vmcreate.KindTools,
		Title:  "пакеты для создания машин",
		Queue:  "host",
		Author: user,
		Steps:  2,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vm.tools.install", "", "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

func (s *Server) handleVMImageDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ImageID string `json:"image_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	img, ok := vmimage.ByID(req.ImageID)
	if !ok {
		writeError(w, http.StatusBadRequest, "нет такого образа в каталоге")
		return
	}
	if s.jobs == nil || s.vmimages == nil {
		writeError(w, http.StatusServiceUnavailable, "фоновые задания недоступны")
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind:  vmimage.KindDownload,
		Title: "образ " + img.Name,
		// Свой ключ очереди: качать образ можно параллельно с работой на
		// хосте — сеть и диск это выдержат, а ждать полчаса, пока
		// освободится общая очередь, незачем.
		Queue:  "vmimage",
		Author: user,
		Steps:  3,
		Params: vmimage.DownloadParams{ImageID: img.ID},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vmimage.download", img.ID, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"job_id": id})
}

func (s *Server) handleVMImageDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ImageID string `json:"image_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	img, ok := vmimage.ByID(req.ImageID)
	if !ok || s.vmimages == nil {
		writeError(w, http.StatusBadRequest, "нет такого образа в каталоге")
		return
	}
	user := auth.Username(r.Context())
	if err := s.vmimages.Delete(img); err != nil {
		s.db.Audit(r.Context(), user, "vmimage.delete", img.ID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vmimage.delete", img.ID, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVMAddress отвечает, какой адрес libvirt знает у машины.
//
// Отдельно от создания: адрес появляется не сразу — сначала машина
// грузится, потом получает его у DHCP, — и спросить его позже нужно и
// хабу, и оператору.
func (s *Server) handleVMAddress(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "не указано имя машины")
		return
	}
	if s.vmimages == nil {
		writeError(w, http.StatusServiceUnavailable, "работа с машинами недоступна")
		return
	}
	runner := vmcreate.NewCreateRunner(s.vmimages, s.scanner.Collector(), RunUnrestricted)
	writeJSON(w, http.StatusOK, map[string]string{
		"name":    name,
		"address": runner.Address(r.Context(), name),
	})
}

func (s *Server) handleVMCreate(w http.ResponseWriter, r *http.Request) {
	var spec vmcreate.Spec
	if err := decodeJSON(r, &spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Ключа нет — заводим свой. Облачный образ приходит без пароля, и
	// машина без единого ключа осталась бы доступной только через
	// консоль; приватная половина уйдёт в ответ и больше нигде не
	// сохранится.
	var generatedKey string
	if strings.TrimSpace(spec.SSHKey) == "" {
		priv, pub, err := vmcreate.GenerateKeyPair(spec.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		spec.SSHKey, generatedKey = pub, priv
	}
	if err := spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, "фоновые задания недоступны")
		return
	}
	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind:  vmcreate.KindCreate,
		Title: "машина " + spec.Name,
		// Ключ очереди — хост: создание машины занимает диск и вызывает
		// virsh, и делать это парой параллельных заданий незачем.
		Queue:  "host",
		Author: user,
		Steps:  5,
		Params: vmcreate.CreateParams{Spec: spec},
	})
	if err != nil {
		s.db.Audit(r.Context(), user, "vm.create", spec.Name, "error", err.Error())
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vm.create", spec.Name, "ok", spec.ImageID)
	// Приватный ключ отдаётся ровно здесь и больше нигде: хранить его в
	// базе значило бы держать ключ от всех созданных машин рядом с ними.
	out := map[string]any{"job_id": id}
	if generatedKey != "" {
		out["private_key"] = generatedKey
		out["public_key"] = spec.SSHKey
	}
	writeJSON(w, http.StatusOK, out)
}

// Шаблон — тот же профиль, только про железо: «2 ядра, 4 ГБ, 20 ГБ,
// Debian 13» под своим именем. Хранится описанием целиком, поэтому
// новое поле формы не требует ни миграции, ни правки этих обработчиков.

func (s *Server) handleVMTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListVMTemplates(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": list})
}

func (s *Server) handleVMTemplateSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string        `json:"name"`
		Spec vmcreate.Spec `json:"spec"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "у шаблона должно быть имя")
		return
	}
	// Имя машины в шаблоне не хранится: шаблон описывает, какая машина, а
	// не какая именно — имя вводят при создании.
	req.Spec.Name = "template"
	if err := req.Spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Spec.Name = ""

	raw, err := json.Marshal(req.Spec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	user := auth.Username(r.Context())
	id, err := s.db.SaveVMTemplate(r.Context(), store.VMTemplate{
		Name: name, Spec: string(raw), Author: user,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vm.template.save", name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleVMTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "неверный номер шаблона")
		return
	}
	tpl, err := s.db.VMTemplateByID(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.db.DeleteVMTemplate(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), auth.Username(r.Context()), "vm.template.delete", tpl.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
