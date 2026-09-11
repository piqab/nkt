package api

import (
	"encoding/json"
	"net/http"
	"path"
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
	tools := vmcreate.CheckTools(r.Context(), RunTooling)
	writeJSON(w, http.StatusOK, map[string]any{
		"catalog": vmimage.Catalog,
		"local":   s.vmimages.Status(),
		"custom":  s.vmimages.Custom(),
		// Своим образам своё состояние: в каталоге их нет, а размер и
		// дата нужны так же.
		"custom_local": s.vmimages.CustomStatus(),
		"dir":          s.vmimages.Dir(),
		// Файлы каталога дисков libvirt: и образы, положенные туда
		// руками, и диски существующих машин — с пометкой, чьи они.
		"host_images": vmcreate.HostImages(r.Context(), RunTooling),
		"tools":       tools,
		"missing":     vmcreate.MissingTools(tools),
	})
}

// maxUploadBytes — потолок загружаемого образа. Облачные образы весят
// сотни мегабайт; десять гигабайт — это уже не образ, а чья-то ошибка.
const maxUploadBytes = 10 << 30

// handleVMImageUpload принимает образ файлом из браузера.
//
// Тело запроса — сам файл, без multipart: образ весит сотни мегабайт, и
// разбирать такую форму в памяти незачем, когда нужно просто записать
// поток на диск.
func (s *Server) handleVMImageUpload(w http.ResponseWriter, r *http.Request) {
	if s.vmimages == nil {
		writeError(w, http.StatusServiceUnavailable, "работа с образами недоступна")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "не указано имя файла")
		return
	}
	user := auth.Username(r.Context())
	// Сначала во временный файл каталога данных (туда писать можно
	// изнутри юнита), потом переносом в каталог дисков libvirt — там
	// его и ждут qemu и оператор.
	tmpPath, err := s.vmimages.SaveTemp(name, http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if err != nil {
		s.db.Audit(r.Context(), user, "vmimage.upload", name, "error", err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := vmcreate.PutHostImage(r.Context(), RunTooling, tmpPath, name)
	if err != nil {
		s.vmimages.RemoveTemp(tmpPath)
		s.db.Audit(r.Context(), user, "vmimage.upload", name, "error", err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vmimage.upload", name, "ok", target)
	writeJSON(w, http.StatusOK, map[string]any{"path": target})
}

// handleVMToolsInstall доставляет пакеты, без которых машину не создать.
// handleVMHostImageDelete убирает файл из каталога дисков libvirt.
func (s *Server) handleVMHostImageDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user := auth.Username(r.Context())
	if err := vmcreate.DeleteHostImage(r.Context(), RunTooling, strings.TrimSpace(req.Name)); err != nil {
		s.db.Audit(r.Context(), user, "vmimage.hostDelete", req.Name, "error", err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vmimage.hostDelete", req.Name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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
		// Свой образ по ссылке: URL и имя файла, под которым он ляжет в
		// кэш. Сумма необязательна — если её нет, образ берётся как
		// есть, о чём задание говорит вслух.
		URL          string `json:"url"`
		FileName     string `json:"file_name"`
		Checksum     string `json:"checksum"`
		ChecksumKind string `json:"checksum_kind"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.jobs == nil || s.vmimages == nil {
		writeError(w, http.StatusServiceUnavailable, "фоновые задания недоступны")
		return
	}

	title := ""
	params := vmimage.DownloadParams{
		ImageID: req.ImageID, URL: strings.TrimSpace(req.URL),
		FileName: strings.TrimSpace(req.FileName),
		Checksum: strings.TrimSpace(req.Checksum), ChecksumKind: req.ChecksumKind,
		// Свой образ по ссылке кладётся туда же, куда и загруженный
		// файлом, — в каталог дисков libvirt. Каталожные остаются в
		// кэше nkt: он сам их скачал, сам и чистит.
		ToHost: strings.TrimSpace(req.URL) != "",
	}
	if params.URL != "" {
		if !strings.HasPrefix(params.URL, "http://") && !strings.HasPrefix(params.URL, "https://") {
			writeError(w, http.StatusBadRequest, "ссылка должна начинаться с http:// или https://")
			return
		}
		if params.FileName == "" {
			// Имя можно взять из самой ссылки — это то, чего оператор и
			// ожидает, вводя URL на .qcow2.
			params.FileName = path.Base(params.URL)
		}
		title = "образ " + params.FileName
	} else {
		img, ok := vmimage.ByID(req.ImageID)
		if !ok {
			writeError(w, http.StatusBadRequest, "нет такого образа в каталоге")
			return
		}
		title = "образ " + img.Name
	}

	user := auth.Username(r.Context())
	id, err := s.jobs.Start(r.Context(), jobs.Spec{
		Kind:  vmimage.KindDownload,
		Title: title,
		// Свой ключ очереди: качать образ можно параллельно с работой на
		// хосте — сеть и диск это выдержат, а ждать полчаса, пока
		// освободится общая очередь, незачем.
		Queue:  "vmimage",
		Author: user,
		Steps:  3,
		Params: params,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vmimage.download", title, "ok", params.URL)
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
	if s.vmimages == nil {
		writeError(w, http.StatusServiceUnavailable, "работа с образами недоступна")
		return
	}
	user := auth.Username(r.Context())
	// Свой образ удаляется по имени файла: в каталоге его нет.
	if name, ok := strings.CutPrefix(req.ImageID, vmimage.CustomPrefix); ok {
		if err := s.vmimages.DeleteCustom(name); err != nil {
			s.db.Audit(r.Context(), user, "vmimage.delete", name, "error", err.Error())
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.db.Audit(r.Context(), user, "vmimage.delete", name, "ok", nil)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	img, ok := vmimage.ByID(req.ImageID)
	if !ok {
		writeError(w, http.StatusBadRequest, "нет такого образа в каталоге")
		return
	}
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
	runner := vmcreate.NewCreateRunner(s.vmimages, s.scanner.Collector(), RunTooling)
	report := runner.AddressReport(r.Context(), name)
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    name,
		"address": report.Address,
		"state":   report.State,
		"reason":  report.Reason,
		"detail":  report.Detail,
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
