package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/vmnet"
)

// Сети libvirt — то, без чего машина не стартует вовсе. Управление ими
// здесь, а не в общем разделе сети хоста: там речь про интерфейсы самого
// сервера, а это — про то, куда включаются его машины.

func (s *Server) vmnets() *vmnet.Manager {
	if s.vmimages == nil {
		return nil
	}
	// Описание сети пишется в каталог кэша образов: он точно доступен на
	// запись из юнита, а /tmp у юнита свой (PrivateTmp) и virsh по ту
	// сторону песочницы его не увидит.
	return vmnet.NewManager(vmnet.Runner(RunTooling), s.vmimages.Dir())
}

func (s *Server) handleVMNetworks(w http.ResponseWriter, r *http.Request) {
	mgr := s.vmnets()
	if mgr == nil {
		writeError(w, http.StatusServiceUnavailable, "управление машинами недоступно")
		return
	}
	nets, err := mgr.List(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": nets})
}

// handleVMFreeSubnet подсказывает форме создания свободную подсеть.
//
// Подставлять в форму постоянную «192.168.100.0/24» было хуже, чем не
// подставлять ничего: значение выглядит проверенным, а на деле может
// пересекаться с уже существующей сетью — и такая сеть не поднимется.
func (s *Server) handleVMFreeSubnet(w http.ResponseWriter, r *http.Request) {
	mgr := s.vmnets()
	if mgr == nil {
		writeError(w, http.StatusServiceUnavailable, "управление машинами недоступно")
		return
	}
	subnet, bridge, err := mgr.Suggest(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"subnet": subnet, "bridge": bridge})
}

func (s *Server) handleVMNetworkCreate(w http.ResponseWriter, r *http.Request) {
	var spec vmnet.Spec
	if err := decodeJSON(r, &spec); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mgr := s.vmnets()
	if mgr == nil {
		writeError(w, http.StatusServiceUnavailable, "управление машинами недоступно")
		return
	}
	user := auth.Username(r.Context())
	if err := mgr.Create(r.Context(), spec); err != nil {
		s.db.Audit(r.Context(), user, "vmnet.create", spec.Name, "error", err.Error())
		// Пересечение с сетью самого хоста оператор может снять
		// осознанно — форма покажет галочку «создать всё равно» только
		// по этому признаку, а не по разбору текста ошибки.
		var inUse *vmnet.SubnetInUse
		if errors.As(err, &inUse) {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":       err.Error(),
				"overridable": inUse.Overridable(),
			})
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vmnet.create", spec.Name, "ok", spec.Mode)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleVMNetworkAction выполняет действие над сетью: поднять,
// остановить, автозапуск, удалить.
func (s *Server) handleVMNetworkAction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	action := chi.URLParam(r, "action")
	mgr := s.vmnets()
	if mgr == nil {
		writeError(w, http.StatusServiceUnavailable, "управление машинами недоступно")
		return
	}

	var err error
	switch action {
	case "start":
		err = mgr.Start(r.Context(), name)
	case "stop":
		err = mgr.Stop(r.Context(), name)
	case "autostart-on":
		err = mgr.SetAutostart(r.Context(), name, true)
	case "autostart-off":
		err = mgr.SetAutostart(r.Context(), name, false)
	case "delete":
		err = mgr.Delete(r.Context(), name)
	default:
		writeError(w, http.StatusBadRequest, "неизвестное действие над сетью")
		return
	}

	user := auth.Username(r.Context())
	if err != nil {
		s.db.Audit(r.Context(), user, "vmnet."+action, name, "error", err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.db.Audit(r.Context(), user, "vmnet."+action, name, "ok", nil)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
