package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/althq/netknownsthat/internal/collect"
	"github.com/althq/netknownsthat/internal/store"
)

// PodmanManager performs actions against the local Podman engine. It is a
// separate manager from ServiceManager's docker path — Podman is a distinct
// engine, not a client for Docker's — mirroring how the two are kept apart
// throughout the rest of the application (own model type, own collector
// method, own parser).
type PodmanManager struct {
	c  collect.Collector
	db *store.DB
}

// NewPodmanManager builds the Podman control plane.
func NewPodmanManager(c collect.Collector, db *store.DB) *PodmanManager {
	return &PodmanManager{c: c, db: db}
}

// ContainerAction starts, stops or restarts a Podman container.
func (m *PodmanManager) ContainerAction(ctx context.Context, user, name, action string) error {
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("недопустимое действие для контейнера: %q", action)
	}
	if name == "" || strings.ContainsAny(name, "/?&#") {
		return fmt.Errorf("недопустимое имя контейнера: %q", name)
	}

	_, code, err := m.c.PodmanAPI(ctx, "POST", "/libpod/containers/"+name+"/"+action, nil)
	outcome := "ok"
	if err != nil || (code != 204 && code != 304) {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "podman."+action, name, outcome, map[string]any{"http_status": code})
	if err != nil {
		return fmt.Errorf("podman %s %s: %w", action, name, err)
	}
	if code != 204 && code != 304 {
		return fmt.Errorf("podman %s %s: HTTP %d", action, name, code)
	}
	return nil
}

// CreateContainer creates a Podman container from an image and starts it —
// the two-step libpod equivalent of `podman run -d --name <name> <image>`.
func (m *PodmanManager) CreateContainer(ctx context.Context, user, image, name string) error {
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("укажите образ")
	}
	if name == "" || strings.ContainsAny(name, "/?&# ") {
		return fmt.Errorf("недопустимое имя контейнера: %q", name)
	}

	body, err := json.Marshal(map[string]string{"image": image, "name": name})
	if err != nil {
		return fmt.Errorf("сборка запроса: %w", err)
	}
	raw, code, err := m.c.PodmanAPI(ctx, "POST", "/libpod/containers/create", body)
	outcome := "ok"
	if err != nil || code != 201 {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "podman.create", name, outcome, map[string]any{"image": image, "http_status": code})
	if err != nil {
		return fmt.Errorf("podman create %s: %w", name, err)
	}
	if code != 201 {
		return fmt.Errorf("podman create %s: HTTP %d", name, code)
	}

	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		return fmt.Errorf("podman create %s: не удалось прочитать ID нового контейнера", name)
	}
	if err := m.ContainerAction(ctx, user, created.ID, "start"); err != nil {
		return fmt.Errorf("контейнер %s создан, но не запущен: %w", name, err)
	}
	return nil
}

// DeleteContainer removes a Podman container. force also stops a running one
// first (the same "graceful vs forced" distinction the rest of this
// application makes explicit rather than silently escalating).
func (m *PodmanManager) DeleteContainer(ctx context.Context, user, name string, force bool) error {
	if name == "" || strings.ContainsAny(name, "/?&#") {
		return fmt.Errorf("недопустимое имя контейнера: %q", name)
	}

	path := "/libpod/containers/" + name
	if force {
		path += "?force=true"
	}
	_, code, err := m.c.PodmanAPI(ctx, "DELETE", path, nil)
	outcome := "ok"
	if err != nil || (code != 200 && code != 204) {
		outcome = "error"
	}
	m.db.Audit(ctx, user, "podman.delete", name, outcome, map[string]any{"force": force, "http_status": code})
	if err != nil {
		return fmt.Errorf("podman delete %s: %w", name, err)
	}
	if code != 200 && code != 204 {
		return fmt.Errorf("podman delete %s: HTTP %d", name, code)
	}
	return nil
}
