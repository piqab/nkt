package store

import (
	"context"
	"database/sql"
	"errors"
)

// Cluster — кластер Kubernetes на виртуалках одного хоста.
type Cluster struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	HostID   int64  `json:"host_id"`
	Flavor   string `json:"flavor"`   // k3s | kubeadm
	Topology string `json:"topology"` // single | cp1 | cp3
	Workers  int    `json:"workers"`
	Expose   bool   `json:"expose"`
	Status   string `json:"status"` // creating | ready | failed | deleting
	ErrorMsg string `json:"error_msg,omitempty"`
	// ServerAddr — адрес API в выданном kubeconfig.
	ServerAddr    string `json:"server_addr,omitempty"`
	KubeconfigEnc []byte `json:"-"`
	// SpecJSON — параметры машин (образ, размеры, сеть) — для «добавить
	// worker» с теми же настройками.
	SpecJSON string `json:"-"`
	// WGEnc — зашифрованный план туннеля WireGuard (ключи, адреса) в
	// режиме сети «wireguard»; пусто в остальных.
	WGEnc     []byte `json:"-"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const (
	ClusterCreating = "creating"
	ClusterReady    = "ready"
	ClusterFailed   = "failed"
	ClusterDeleting = "deleting"
)

const clusterColumns = `id, name, host_id, flavor, topology, workers, expose, status, error_msg, server_addr, kubeconfig_enc, spec_json, wg_enc, created_at, updated_at`

func scanCluster(row interface{ Scan(...any) error }) (Cluster, error) {
	var c Cluster
	var kc, wg []byte
	err := row.Scan(&c.ID, &c.Name, &c.HostID, &c.Flavor, &c.Topology, &c.Workers, &c.Expose, &c.Status, &c.ErrorMsg,
		&c.ServerAddr, &kc, &c.SpecJSON, &wg, &c.CreatedAt, &c.UpdatedAt)
	c.KubeconfigEnc, c.WGEnc = kc, wg
	return c, err
}

// CreateCluster заводит запись со статусом creating.
func (d *DB) CreateCluster(ctx context.Context, c Cluster) (int64, error) {
	now := Now()
	res, err := d.ExecContext(ctx, `INSERT INTO clusters(name, host_id, flavor, topology, workers, expose, status, spec_json, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, c.Name, c.HostID, c.Flavor, c.Topology, c.Workers, c.Expose, ClusterCreating, c.SpecJSON, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) ClusterByID(ctx context.Context, id int64) (Cluster, error) {
	c, err := scanCluster(d.QueryRowContext(ctx, `SELECT `+clusterColumns+` FROM clusters WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Cluster{}, ErrNotFound
	}
	return c, err
}

func (d *DB) ListClusters(ctx context.Context) ([]Cluster, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+clusterColumns+` FROM clusters ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Cluster{}
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetClusterStatus записывает статус и ошибку.
func (d *DB) SetClusterStatus(ctx context.Context, id int64, status, errMsg string) error {
	_, err := d.ExecContext(ctx, `UPDATE clusters SET status = ?, error_msg = ?, updated_at = ? WHERE id = ?`, status, errMsg, Now(), id)
	return err
}

// SetClusterKubeconfig сохраняет зашифрованный kubeconfig и адрес API.
func (d *DB) SetClusterKubeconfig(ctx context.Context, id int64, serverAddr string, enc []byte) error {
	_, err := d.ExecContext(ctx, `UPDATE clusters SET server_addr = ?, kubeconfig_enc = ?, updated_at = ? WHERE id = ?`, serverAddr, enc, Now(), id)
	return err
}

// SetClusterWG сохраняет зашифрованный план туннеля.
func (d *DB) SetClusterWG(ctx context.Context, id int64, enc []byte) error {
	_, err := d.ExecContext(ctx, `UPDATE clusters SET wg_enc = ?, updated_at = ? WHERE id = ?`, enc, Now(), id)
	return err
}

// SetClusterWorkers обновляет число worker'ов после добавления.
func (d *DB) SetClusterWorkers(ctx context.Context, id int64, n int) error {
	_, err := d.ExecContext(ctx, `UPDATE clusters SET workers = ?, updated_at = ? WHERE id = ?`, n, Now(), id)
	return err
}

func (d *DB) DeleteCluster(ctx context.Context, id int64) error {
	if _, err := d.ExecContext(ctx, `UPDATE hosts SET cluster_id = 0, k8s_role = '' WHERE cluster_id = ?`, id); err != nil {
		return err
	}
	_, err := d.ExecContext(ctx, `DELETE FROM clusters WHERE id = ?`, id)
	return err
}

// SetHostCluster привязывает хост к кластеру с ролью.
func (d *DB) SetHostCluster(ctx context.Context, hostID, clusterID int64, role string) error {
	_, err := d.ExecContext(ctx, `UPDATE hosts SET cluster_id = ?, k8s_role = ? WHERE id = ?`, clusterID, role, hostID)
	return err
}

// ClusterHosts — узлы кластера: control plane первыми, затем по имени.
func (d *DB) ClusterHosts(ctx context.Context, clusterID int64) ([]Host, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+hostColumns+` FROM hosts WHERE cluster_id = ? ORDER BY CASE k8s_role WHEN 'control-plane' THEN 0 ELSE 1 END, name`, clusterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
