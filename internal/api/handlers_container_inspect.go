package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
)

// «Инспект» контейнера Docker: переменные окружения с источником (из
// образа или заданы для контейнера), образ, команда, перезапуск,
// состояние, порты, тома, сети, метки и полный inspect. Значения
// переменных — пароли и ключи: по умолчанию не уходят в браузер вовсе;
// раскрывает их только администратор (?reveal=1), и это пишется в журнал
// действий. В полном JSON значения замазаны так же.

// inspectEnv — переменная окружения контейнера.
type inspectEnv struct {
	Name string `json:"name"`
	// Value — значение; пусто и Masked — скрыто (не раскрыто).
	Value  string `json:"value"`
	Masked bool   `json:"masked,omitempty"`
	// Origin — image (ENV образа, не переопределена), container (задана
	// для контейнера: compose, .env, docker run -e) или override
	// (задана для контейнера поверх значения образа).
	Origin string `json:"origin"`
}

type inspectPort struct {
	Container string `json:"container"`
	HostIP    string `json:"host_ip,omitempty"`
	HostPort  string `json:"host_port,omitempty"`
}

type inspectMount struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	Name        string `json:"name,omitempty"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

type inspectNet struct {
	Name    string   `json:"name"`
	IP      string   `json:"ip,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	MAC     string   `json:"mac,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
}

// dockerInspect — нужные поля GET /containers/{id}/json.
type dockerInspect struct {
	ID      string   `json:"Id"`
	Created string   `json:"Created"`
	Path    string   `json:"Path"`
	Args    []string `json:"Args"`
	State   struct {
		Status     string `json:"Status"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		ExitCode   int    `json:"ExitCode"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Image        string `json:"Image"`
	Name         string `json:"Name"`
	RestartCount int    `json:"RestartCount"`
	HostConfig   struct {
		RestartPolicy struct {
			Name              string `json:"Name"`
			MaximumRetryCount int    `json:"MaximumRetryCount"`
		} `json:"RestartPolicy"`
		Memory   int64 `json:"Memory"`
		NanoCpus int64 `json:"NanoCpus"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	Config struct {
		User       string            `json:"User"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		Image      string            `json:"Image"`
		WorkingDir string            `json:"WorkingDir"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
	NetworkSettings struct {
		Ports    map[string][]struct{ HostIp, HostPort string } `json:"Ports"`
		Networks map[string]struct {
			IPAddress  string   `json:"IPAddress"`
			Gateway    string   `json:"Gateway"`
			MacAddress string   `json:"MacAddress"`
			Aliases    []string `json:"Aliases"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// splitEnv — «ИМЯ=значение» в карту и порядок имён.
func splitEnv(list []string) (map[string]string, []string) {
	m := map[string]string{}
	var order []string
	for _, kv := range list {
		k, v, _ := strings.Cut(kv, "=")
		if _, seen := m[k]; !seen {
			order = append(order, k)
		}
		m[k] = v
	}
	return m, order
}

// envOrigins — переменные контейнера с источником относительно образа.
func envOrigins(container, image []string, reveal bool) []inspectEnv {
	cm, order := splitEnv(container)
	im, _ := splitEnv(image)
	out := make([]inspectEnv, 0, len(order))
	for _, k := range order {
		e := inspectEnv{Name: k, Value: cm[k], Origin: "container"}
		if iv, ok := im[k]; ok {
			e.Origin = "override"
			if iv == cm[k] {
				e.Origin = "image"
			}
		}
		if !reveal {
			e.Value, e.Masked = "", true
		}
		out = append(out, e)
	}
	// Сначала заданные для контейнера — их обычно и ищут.
	rank := map[string]int{"container": 0, "override": 1, "image": 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Origin] < rank[out[j].Origin] })
	return out
}

// maskRawEnv — полный inspect с замазанными значениями Config.Env.
func maskRawEnv(raw []byte) json.RawMessage {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	if cfg, ok := doc["Config"].(map[string]any); ok {
		if env, ok := cfg["Env"].([]any); ok {
			for i, v := range env {
				if s, ok := v.(string); ok {
					k, _, _ := strings.Cut(s, "=")
					env[i] = k + "=••••••"
				}
			}
		}
	}
	out, _ := json.Marshal(doc)
	return out
}

// handleContainerInspect — GET /containers/{name}/inspect[?reveal=1].
func (s *Server) handleContainerInspect(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !containerNameRe.MatchString(name) {
		writeError(w, http.StatusBadRequest, msgs.T(msgs.LangFromRequest(r), "control.invalidContainerName", name))
		return
	}
	ctx := r.Context()
	reveal := r.URL.Query().Get("reveal") == "1"
	if reveal {
		user, _ := auth.UserFromContext(ctx)
		if !user.IsAdmin() {
			writeError(w, http.StatusForbidden, msgs.T(msgs.LangFromRequest(r), "docker.inspectRevealAdmin"))
			return
		}
	}
	c := s.scanner.Collector()
	raw, code, err := c.DockerAPI(ctx, "GET", "/containers/"+url.PathEscape(name)+"/json", nil)
	if err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	if code == http.StatusNotFound {
		writeError(w, http.StatusNotFound, msgs.T(msgs.LangFromRequest(r), "docker.inspectNotFound", name))
		return
	}
	if code != http.StatusOK {
		writeError(w, http.StatusBadGateway, strings.TrimSpace(string(raw)))
		return
	}
	var d dockerInspect
	if err := json.Unmarshal(raw, &d); err != nil {
		writeErr(w, r, http.StatusBadGateway, err)
		return
	}
	// ENV образа — чтобы отличить свои переменные от унаследованных; тег
	// образа, если он теперь указывает на другой образ, — «есть новее».
	var imageEnv []string
	if ir, code, err := c.DockerAPI(ctx, "GET", "/images/"+url.PathEscape(d.Image)+"/json", nil); err == nil && code == http.StatusOK {
		var img struct {
			Config struct {
				Env []string `json:"Env"`
			} `json:"Config"`
		}
		_ = json.Unmarshal(ir, &img)
		imageEnv = img.Config.Env
	}
	tagImage := ""
	if d.Config.Image != "" && !strings.HasPrefix(d.Config.Image, "sha256:") {
		if ir, code, err := c.DockerAPI(ctx, "GET", "/images/"+url.PathEscape(d.Config.Image)+"/json", nil); err == nil && code == http.StatusOK {
			var img struct {
				ID string `json:"Id"`
			}
			_ = json.Unmarshal(ir, &img)
			tagImage = img.ID
		}
	}

	var ports []inspectPort
	for cport, binds := range d.NetworkSettings.Ports {
		if len(binds) == 0 {
			ports = append(ports, inspectPort{Container: cport})
		}
		for _, b := range binds {
			ports = append(ports, inspectPort{Container: cport, HostIP: b.HostIp, HostPort: b.HostPort})
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Container+ports[i].HostIP < ports[j].Container+ports[j].HostIP })
	mounts := make([]inspectMount, 0, len(d.Mounts))
	for _, m := range d.Mounts {
		mounts = append(mounts, inspectMount{Type: m.Type, Source: m.Source, Name: m.Name, Destination: m.Destination, RW: m.RW})
	}
	nets := make([]inspectNet, 0, len(d.NetworkSettings.Networks))
	for n, v := range d.NetworkSettings.Networks {
		nets = append(nets, inspectNet{Name: n, IP: v.IPAddress, Gateway: v.Gateway, MAC: v.MacAddress, Aliases: v.Aliases})
	}
	sort.Slice(nets, func(i, j int) bool { return nets[i].Name < nets[j].Name })
	health := ""
	if d.State.Health != nil {
		health = d.State.Health.Status
	}
	full := json.RawMessage(raw)
	if !reveal {
		full = maskRawEnv(raw)
	}
	if reveal {
		s.db.Audit(ctx, auth.Username(ctx), "container.inspect_reveal", name, "ok", nil)
	}
	labels := d.Config.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	if ports == nil {
		ports = []inspectPort{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": strings.TrimPrefix(d.Name, "/"), "id": d.ID, "created": d.Created,
		"image": d.Config.Image, "image_id": d.Image,
		// Тег теперь указывает на другой образ — контейнер на старом
		// (новый образ загружен, а контейнер не пересоздан).
		"image_outdated": tagImage != "" && tagImage != d.Image,
		"state":          d.State.Status, "started_at": d.State.StartedAt, "finished_at": d.State.FinishedAt,
		"exit_code": d.State.ExitCode, "health": health, "restart_count": d.RestartCount,
		"restart_policy": d.HostConfig.RestartPolicy.Name, "restart_retries": d.HostConfig.RestartPolicy.MaximumRetryCount,
		"memory_limit": d.HostConfig.Memory, "nano_cpus": d.HostConfig.NanoCpus,
		"entrypoint": d.Config.Entrypoint, "cmd": d.Config.Cmd, "user": d.Config.User, "working_dir": d.Config.WorkingDir,
		"compose_project": labels["com.docker.compose.project"], "compose_service": labels["com.docker.compose.service"],
		"env": envOrigins(d.Config.Env, imageEnv, reveal), "revealed": reveal,
		"ports": ports, "mounts": mounts, "networks": nets, "labels": labels,
		"raw": full,
	})
}
