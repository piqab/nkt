package hub

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/piqab/nkt/internal/auth"
	"github.com/piqab/nkt/internal/msgs"
	"github.com/piqab/nkt/internal/store"
	"github.com/piqab/nkt/internal/vmcreate"
)

// Полоса и диск машин токенов по умолчанию (1.17.14): пустой потолок у
// токена — значения хаба; задаёт их администратор («О системе» →
// API-токены → «Потолки машин по умолчанию»). Машинам администратора —
// без потолков.

const vmLimitsKV = "vm.limits.defaults"

// VMLimitDefaults — значения по умолчанию.
type VMLimitDefaults struct {
	NetMbit  int `json:"net_mbit"`
	IOPS     int `json:"iops"`
	DiskMBps int `json:"disk_mbps"`
}

// vmLimitFallback — пока администратор ничего не задал.
var vmLimitFallback = VMLimitDefaults{NetMbit: 100, IOPS: 1000, DiskMBps: 100}

func vmLimitDefaults(ctx context.Context, db *store.DB) VMLimitDefaults {
	d := vmLimitFallback
	if raw, ok, _ := db.KVGet(ctx, vmLimitsKV); ok {
		_ = json.Unmarshal([]byte(raw), &d)
	}
	return d
}

// effLimit — потолок токена или значение хаба.
func effLimit(tok, def int) int {
	if tok > 0 {
		return tok
	}
	return def
}

// applyTokenVMLimits — полоса и диск машины токена.
func applyTokenVMLimits(t store.APIToken, d VMLimitDefaults, spec *vmcreate.Spec) {
	l := t.Perms.Limits
	spec.NetMbit, spec.IOPS, spec.DiskMBps = effLimit(l.VMNetMbit, d.NetMbit), effLimit(l.VMIOPS, d.IOPS), effLimit(l.VMDiskMBps, d.DiskMBps)
}

// handleVMLimitDefaults — GET/PUT /hub/vm/limit-defaults.
func (s *Server) handleVMLimitDefaults(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cur := vmLimitDefaults(ctx, s.db)
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, cur)
		return
	}
	var req VMLimitDefaults
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, r, http.StatusBadRequest, err)
		return
	}
	if req.NetMbit < 1 || req.NetMbit > 100000 || req.IOPS < 1 || req.IOPS > 1000000 || req.DiskMBps < 1 || req.DiskMBps > 100000 {
		writeErr(w, r, http.StatusBadRequest, msgs.Errorf("vmcreate.badIOLimits"))
		return
	}
	raw, _ := json.Marshal(req)
	if err := s.db.KVSet(ctx, vmLimitsKV, string(raw)); err != nil {
		fail(w, r, err)
		return
	}
	s.db.Audit(ctx, auth.Username(ctx), "vm.limit_defaults", "", "ok", map[string]any{"before": cur, "after": req})
	writeJSON(w, http.StatusOK, req)
}
