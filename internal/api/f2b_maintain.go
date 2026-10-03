package api

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/fail2ban"
	"github.com/piqab/nkt/internal/msgs"
)

// Свои файлы fail2ban nkt обновляет сам: прежние версии бывали неверны
// (фильтр nkt-manual 1.11.x без <HOST> — fail2ban-client -t его
// пропускает, а reload падает целиком и оставляет сервер без джейлов,
// в том числе без sshd). Чужие файлы не трогаются.

// f2bMaintainEvery — как часто сверять свои файлы.
const f2bMaintainEvery = 30 * time.Minute

// f2bMaintainDelay — первая сверка после запуска (служба уже поднялась).
const f2bMaintainDelay = 30 * time.Second

// f2bMaintainUser — от чьего имени запись (в журнале действий).
const f2bMaintainUser = "nkt"

// StartMaintenance — фоновые сверки хоста до ctx.Done().
func (s *Server) StartMaintenance(ctx context.Context) {
	go func() {
		t := time.NewTimer(f2bMaintainDelay)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if err := s.f2bRefreshOwnFiles(ctx); err != nil && s.log != nil {
				s.log.Warn("fail2ban: own files not refreshed", "err", err)
			}
			t.Reset(f2bMaintainEvery)
		}
	}()
}

// f2bRefreshOwnFiles — устаревший фильтр ручного джейла переписать и,
// если fail2ban работает, перезагрузить: джейлы, которых не было, должны
// подняться. Итог — в журнале действий.
func (s *Server) f2bRefreshOwnFiles(ctx context.Context) error {
	if s.cfg == nil || s.configs == nil || s.scanner == nil || s.cfg.IsFixtures() || strings.Trim(s.f2bRoot(), "/") == "" {
		return nil
	}
	c := s.f2bCollector()
	if !fail2ban.Installed(ctx, c) {
		return nil
	}
	full := path.Join(s.f2bRoot(), fail2ban.ManualFilterFile)
	raw, err := c.ReadFile(full)
	if err != nil || string(raw) == fail2ban.ManualFilterContent {
		// Нет файла — ручной джейл ещё не заводили; совпадает — делать
		// нечего.
		return nil
	}
	ctx = msgs.WithLang(ctx, msgs.DefaultLang)
	before := fail2ban.CollectBans(ctx, c)
	note := msgs.Tc(ctx, "f2b.noteOwnRefresh")
	if _, err := s.configs.Write(ctx, msgs.DefaultLang, f2bMaintainUser, full, fail2ban.ManualFilterContent, note, false); err != nil {
		s.db.Audit(ctx, f2bMaintainUser, "fail2ban.own_refresh", fail2ban.ManualFilterFile, "error", err.Error())
		return err
	}
	detail := ""
	if before.Running {
		if _, err := fail2ban.Reload(ctx, c, ""); err != nil {
			s.db.Audit(ctx, f2bMaintainUser, "fail2ban.own_refresh", fail2ban.ManualFilterFile, "error", err.Error())
			s.f2bInvalidate()
			return err
		}
		after := fail2ban.CollectBans(ctx, c)
		detail = strings.Join(fail2ban.RunningJails(after), ", ")
	}
	s.db.Audit(ctx, f2bMaintainUser, "fail2ban.own_refresh", fail2ban.ManualFilterFile, "ok", detail)
	s.f2bInvalidate()
	s.rescanLater()
	return nil
}
