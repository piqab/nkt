package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

// Роли API-токена: чтение — состояние хостов, проблемы, оповещения,
// журналы заданий; администратор — ещё и действия (выкладка, бан IP,
// службы). Управлять хабом (учётки, токены, экспорт, обновление) токен
// не может ни с какой ролью.
const (
	TokenRoleRead  = "read"
	TokenRoleAdmin = "admin"
	// TokenRoleCustom — «по правам»: чтение плюс только перечисленные
	// действия (Perms.Actions), с пределами и потолками ресурсов.
	TokenRoleCustom = "custom"
)

// Действия токена «по правам».
//
// #nosec G101 -- названия действий (pipeline.deploy, vuln.scan), а не учётные данные.
const (
	TokenActPipelineDeploy = "pipeline.deploy" // выкладка, откат, сухой прогон
	TokenActPipelinePower  = "pipeline.power"  // «Запустить» / «Остановить»
	TokenActPipelineCreate = "pipeline.create" // создать, править и удалить свои конвейеры (compose)
	TokenActScriptRun      = "script.run"      // запуск сохранённых сценариев
	TokenActK8sApply       = "k8s.apply"       // манифесты в namespace токена
	TokenActVMManage       = "vm.manage"       // машины: создать, включить, выключить
	TokenActContainer      = "container.action"
	TokenActService        = "service.action"
	TokenActFly            = "fly.manage"
	TokenActMigrate        = "migrate"
	TokenActFail2ban       = "fail2ban.manage"
	TokenActVulnScan       = "vuln.scan"
	// TokenActK8sCluster — свой кластер Kubernetes (создать, удалить) — в
	// сценариях и MCP; без него «k8s create» токену недоступен.
	TokenActK8sCluster = "k8s.cluster"
	// TokenActVMPorts — порты хоста из пула на свои машины (проброс).
	TokenActVMPorts = "vm.ports"
	// TokenActFlyOwn — свои маршруты Traffic Fly: имена в выданном домене
	// (FlyDomain), цели — свои машины. Карту целиком не правит.
	TokenActFlyOwn = "fly.own"
)

// TokenActions — все действия по порядку (окно токена, проверка).
var TokenActions = []string{TokenActPipelineDeploy, TokenActPipelinePower, TokenActPipelineCreate, TokenActScriptRun, TokenActK8sApply, TokenActVMManage,
	TokenActContainer, TokenActService, TokenActFly, TokenActMigrate, TokenActFail2ban, TokenActVulnScan, TokenActK8sCluster, TokenActVMPorts, TokenActFlyOwn}

// TokenPerms — права и потолки токена «по правам».
type TokenPerms struct {
	Actions []string `json:"actions"`
	// Pipelines / Scripts / Clusters — по именам; пусто — все в пределах
	// хостов токена.
	Pipelines []string `json:"pipelines,omitempty"`
	Scripts   []string `json:"scripts,omitempty"`
	Clusters  []string `json:"clusters,omitempty"`
	// Namespaces — namespace k8s, куда токену можно (обязательно для
	// k8s.apply).
	Namespaces []string `json:"namespaces,omitempty"`
	// FlyDomain — домен для своих маршрутов Traffic Fly (сам домен и имена
	// под ним); FlyGateway — шлюз (edge) этих маршрутов, 0 — единственный.
	FlyDomain  string      `json:"fly_domain,omitempty"`
	FlyGateway int64       `json:"fly_gateway,omitempty"`
	Limits     TokenLimits `json:"limits"`
}

// TokenLimits — потолки ресурсов; ноль — без потолка. Суммы — по всему,
// что выложено и создано этим токеном и ещё существует (token_usage).
type TokenLimits struct {
	// Всего: ядра, память, диск (диски машин и хранилище k8s).
	CPU    float64 `json:"cpu,omitempty"`
	MemMB  int64   `json:"mem_mb,omitempty"`
	DiskGB int64   `json:"disk_gb,omitempty"`
	// На один хост (стеки compose и машины на нём).
	HostCPU   float64 `json:"host_cpu,omitempty"`
	HostMemMB int64   `json:"host_mem_mb,omitempty"`
	// compose: потолок сервиса — без своих limits сервис получает его.
	ServiceCPU   float64 `json:"service_cpu,omitempty"`
	ServiceMemMB int64   `json:"service_mem_mb,omitempty"`
	// compose: свободного места на хосте перед выкладкой.
	MinFreeGB int64 `json:"min_free_gb,omitempty"`
	// Машины: сколько всего и потолки одной.
	VMs      int   `json:"vms,omitempty"`
	VMCPU    int   `json:"vm_cpu,omitempty"`
	VMMemMB  int64 `json:"vm_mem_mb,omitempty"`
	VMDiskGB int64 `json:"vm_disk_gb,omitempty"`
	// Снимков на одну машину. В отличие от потолков выше 0 — «нельзя»:
	// снимки занимают диск хоста мимо потолка диска машины.
	VMSnapshots int `json:"vm_snapshots,omitempty"`
	// Портов хоста из пула (на все машины) и своих маршрутов Traffic
	// Fly; 0 — нельзя.
	Ports     int `json:"ports,omitempty"`
	FlyRoutes int `json:"fly_routes,omitempty"`
	// k8s: квота одного namespace (ResourceQuota), подов в нём.
	K8sCPU       float64 `json:"k8s_cpu,omitempty"`
	K8sMemMB     int64   `json:"k8s_mem_mb,omitempty"`
	K8sStorageGB int64   `json:"k8s_storage_gb,omitempty"`
	K8sPods      int     `json:"k8s_pods,omitempty"`
	// Выкладок (и применений манифестов) в час.
	DeploysPerHour int `json:"deploys_per_hour,omitempty"`
}

// Can — токену можно это действие: администратору — всё, «по правам» —
// перечисленное, чтению — ничего.
func (t APIToken) Can(action string) bool {
	switch t.Role {
	case TokenRoleAdmin:
		return true
	case TokenRoleCustom:
		return slices.Contains(t.Perms.Actions, action)
	}
	return false
}

// Owns — объект создан этим токеном (автор «token:<имя>»).
func (t APIToken) Owns(author string) bool { return author == "token:"+t.Name }

// Limited — у токена есть права и потолки (роль «по правам»).
func (t APIToken) Limited() bool { return t.Role == TokenRoleCustom }

// APIToken — токен API хаба. Пустые Hosts и Groups — доступ ко всем
// хостам; иначе — только к перечисленным хостам и хостам групп.
type APIToken struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	KeyID     string   `json:"key_id"`
	Role      string   `json:"role"`
	Hosts     []int64  `json:"hosts"`
	Groups    []string `json:"groups"`
	IPs       []string `json:"ips"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	// ViaEdge — токен принимается и через nkt-edge с ролью api (только
	// подписанные запросы).
	ViaEdge bool `json:"via_edge"`
	// Perms — права и потолки роли «по правам».
	Perms     TokenPerms `json:"perms"`
	Author    string     `json:"author,omitempty"`
	CreatedAt string     `json:"created_at"`
	UpdatedAt string     `json:"updated_at"`
	LastUsed  string     `json:"last_used_at,omitempty"`
	LastIP    string     `json:"last_ip,omitempty"`

	SecretEnc []byte `json:"-"`
}

// Scoped — токен ограничен хостами или группами.
func (t APIToken) Scoped() bool { return len(t.Hosts) > 0 || len(t.Groups) > 0 }

// Expired — срок токена вышел.
func (t APIToken) Expired(now time.Time) bool {
	if t.ExpiresAt == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, t.ExpiresAt)
	return err != nil || !now.Before(exp)
}

// AllowsHost — хост (и его группа) в пределах токена.
func (t APIToken) AllowsHost(id int64, group string) bool {
	if !t.Scoped() {
		return true
	}
	return slices.Contains(t.Hosts, id) || (group != "" && slices.Contains(t.Groups, group))
}

// #nosec G101 -- список колонок SQL (secret_enc — имя колонки), не учётные данные.
const apiTokenCols = `id, name, key_id, secret_enc, role, hosts, groups_json, ips, expires_at, via_edge, author, created_at, updated_at, last_used_at, last_ip, perms`

func scanAPIToken(row interface{ Scan(...any) error }) (APIToken, error) {
	var t APIToken
	var hosts, groups, ips, perms string
	err := row.Scan(&t.ID, &t.Name, &t.KeyID, &t.SecretEnc, &t.Role, &hosts, &groups, &ips, &t.ExpiresAt, &t.ViaEdge, &t.Author,
		&t.CreatedAt, &t.UpdatedAt, &t.LastUsed, &t.LastIP, &perms)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err != nil {
		return t, err
	}
	_ = json.Unmarshal([]byte(hosts), &t.Hosts)
	_ = json.Unmarshal([]byte(groups), &t.Groups)
	_ = json.Unmarshal([]byte(ips), &t.IPs)
	if perms != "" {
		_ = json.Unmarshal([]byte(perms), &t.Perms)
	}
	if t.Perms.Actions == nil {
		t.Perms.Actions = []string{}
	}
	if t.Hosts == nil {
		t.Hosts = []int64{}
	}
	if t.Groups == nil {
		t.Groups = []string{}
	}
	if t.IPs == nil {
		t.IPs = []string{}
	}
	return t, nil
}

func jsonList[T any](v []T) string {
	if v == nil {
		return "[]"
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// CreateAPIToken заводит токен.
func (d *DB) CreateAPIToken(ctx context.Context, t APIToken) (int64, error) {
	now := FormatTime(time.Now())
	res, err := d.ExecContext(ctx, `INSERT INTO api_tokens(name, key_id, secret_enc, role, hosts, groups_json, ips, expires_at, via_edge, author, created_at, updated_at, perms)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Name, t.KeyID, t.SecretEnc, t.Role, jsonList(t.Hosts), jsonList(t.Groups), jsonList(t.IPs), t.ExpiresAt, t.ViaEdge, t.Author, now, now, permsJSON(t))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAPITokens — все токены, новые сверху.
func (d *DB) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// APITokenByID — токен по номеру.
func (d *DB) APITokenByID(ctx context.Context, id int64) (APIToken, error) {
	return scanAPIToken(d.QueryRowContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE id = ?`, id))
}

// APITokenByKeyID — токен по открытой части ключа.
func (d *DB) APITokenByKeyID(ctx context.Context, keyID string) (APIToken, error) {
	return scanAPIToken(d.QueryRowContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE key_id = ?`, keyID))
}

// UpdateAPIToken — имя, роль, пределы, адреса и срок.
func (d *DB) UpdateAPIToken(ctx context.Context, t APIToken) error {
	_, err := d.ExecContext(ctx, `UPDATE api_tokens SET name = ?, role = ?, hosts = ?, groups_json = ?, ips = ?, expires_at = ?, via_edge = ?, updated_at = ?, perms = ? WHERE id = ?`,
		t.Name, t.Role, jsonList(t.Hosts), jsonList(t.Groups), jsonList(t.IPs), t.ExpiresAt, t.ViaEdge, FormatTime(time.Now()), permsJSON(t), t.ID)
	return err
}

// permsJSON — права токена для колонки perms (у прочих ролей — пусто).
func permsJSON(t APIToken) string {
	if t.Role != TokenRoleCustom {
		return ""
	}
	raw, _ := json.Marshal(t.Perms)
	return string(raw)
}

// SetAPITokenSecret — новый секрет (старый перестаёт действовать сразу).
func (d *DB) SetAPITokenSecret(ctx context.Context, id int64, keyID string, secretEnc []byte) error {
	_, err := d.ExecContext(ctx, `UPDATE api_tokens SET key_id = ?, secret_enc = ?, updated_at = ? WHERE id = ?`,
		keyID, secretEnc, FormatTime(time.Now()), id)
	return err
}

// TouchAPIToken — когда и откуда токен использовали в последний раз.
func (d *DB) TouchAPIToken(ctx context.Context, id int64, ip string) error {
	_, err := d.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ?, last_ip = ? WHERE id = ?`, FormatTime(time.Now()), ip, id)
	return err
}

// DeleteAPIToken — отзыв токена.
func (d *DB) DeleteAPIToken(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	if err == nil {
		_, err = d.ExecContext(ctx, `DELETE FROM token_usage WHERE token_id = ?`, id)
	}
	return err
}

// renameGroupInTokens — группа переименована: токены, ограниченные ею,
// остаются при ней.
func renameGroupInTokens(ctx context.Context, tx *sql.Tx, from, to string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, groups_json FROM api_tokens WHERE groups_json <> '[]'`)
	if err != nil {
		return err
	}
	type upd struct {
		id     int64
		groups string
	}
	var todo []upd
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var groups []string
		_ = json.Unmarshal([]byte(raw), &groups)
		if i := slices.Index(groups, from); i >= 0 {
			groups[i] = to
			todo = append(todo, upd{id, jsonList(slices.Compact(groups))})
		}
	}
	rows.Close()
	for _, u := range todo {
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET groups_json = ? WHERE id = ?`, u.groups, u.id); err != nil {
			return err
		}
	}
	return nil
}
