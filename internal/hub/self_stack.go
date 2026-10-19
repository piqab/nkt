package hub

import (
	"context"
	"os"
	"strings"

	"github.com/piqab/nkt/internal/model"
)

// Хаб в контейнере не должен выкладывать или переносить стек, в котором
// работает сам: задание остановило бы контейнер, в котором оно идёт, —
// посередине, с хабом в неизвестном состоянии. Свой контейнер хаб узнаёт
// по имени машины: у контейнера docker это начало его ID.

// hubContainerID — начало ID своего контейнера ("" — хаб не в контейнере).
var hubContainerID = func() string {
	if _, err := os.Stat("/.dockerenv"); err != nil {
		if _, err := os.Stat("/run/.containerenv"); err != nil {
			return ""
		}
	}
	hn, err := os.Hostname()
	if err != nil || len(hn) < 12 || strings.Trim(hn, "0123456789abcdef") != "" {
		return ""
	}
	return hn
}

// hubStackOn — compose-проект, в котором на хосте работает этот хаб (""
// — не здесь или хаб не в контейнере). Хост не ответил — "": проверка
// не должна мешать выкладке, которая сама скажет, что хост недоступен.
func (s *Server) hubStackOn(ctx context.Context, user string, hostID int64) string {
	id := hubContainerID()
	if id == "" || hostID == localHostID {
		return ""
	}
	var cs struct {
		Containers []model.Container `json:"containers"`
	}
	if _, err := s.hostCall(ctx, user, hostID, "GET", "/api/containers", nil, &cs); err != nil {
		return ""
	}
	return hubProjectIn(cs.Containers, id)
}

// hubProjectIn — проект контейнера с ID, начинающимся с id.
func hubProjectIn(list []model.Container, id string) string {
	for _, c := range list {
		if id != "" && strings.HasPrefix(c.ID, id) {
			return c.Project
		}
	}
	return ""
}
