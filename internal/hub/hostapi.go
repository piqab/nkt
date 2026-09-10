package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/piqab/nkt/internal/auth"
)

// Хаб умеет проксировать запросы браузера к хосту (см. Proxy), но иногда
// ему нужно сходить туда самому — без браузера, из фонового задания.
// Здесь тот же путь, что у опроса состояния: канал до хоста, сессия и
// обычный HTTP поверх туннеля.

// hostAPIMaxBody — потолок ответа. Планы и статусы заданий невелики, а
// принимать сюда что угодно с чужой машины незачем.
const hostAPIMaxBody = 8 << 20

// HostAPI выполняет запрос к API управляемого хоста и возвращает код
// ответа с телом.
//
// out, если задан, заполняется разбором JSON-ответа. Ненулевой код — не
// ошибка Go: вызывающий решает сам, что делать с 404 или 503.
func (m *Manager) HostAPI(ctx context.Context, hostID int64, method, path string, in, out any) (int, error) {
	dial, channel, onFail, err := m.dialerFor(ctx, hostID)
	if err != nil {
		return 0, err
	}
	m.recordChannel(hostID, channel)
	cookie, err := m.cookieFor(ctx, hostID, dial)
	if err != nil {
		onFail()
		return 0, err
	}

	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+remoteAPIAddr+path, body)
	if err != nil {
		return 0, err
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := tunnelHTTPClient(dial).Do(req)
	if err != nil {
		onFail()
		return 0, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, hostAPIMaxBody))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("%s %s: код %d: %s", method, path, resp.StatusCode, hostAPIError(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("разбор ответа %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// hostAPIError достаёт человеческую причину из ответа хоста: там всегда
// {"error": "..."} — показывать вместо неё сырой JSON незачем.
func hostAPIError(raw []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error != "" {
		return body.Error
	}
	if len(raw) > 200 {
		raw = raw[:200]
	}
	return string(raw)
}
