package hub

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/piqab/nkt/internal/store"
)

// Сертификаты сайтов выкладок: раз в сутки хаб проверяет каждый
// настроенный сайт снаружи (как кнопка «Проверить») и оповещает, когда
// до конца сертификата остаётся 14, 7, 3 дня и когда он истёк — по разу
// на порог. Продлили — порог забывается.

const (
	certAlertedKV  = "sites.cert_alerted"
	certCheckEvery = 24 * time.Hour
	// certCheckFirst — первая проверка после запуска хаба: не сразу, хаб
	// и edge сперва поднимают соединения.
	certCheckFirst = 10 * time.Minute
)

// certThresholds — пороги оповещения, дней (0 — истёк).
var certThresholds = []int{14, 7, 3, 0}

// certBucket — самый строгий пройденный порог и полных дней до конца;
// порог -1 — до порогов далеко или срок неизвестен. Дни — по дате, а не
// по полю проверки: при ошибке TLS оно не заполняется.
func certBucket(notAfter string, now time.Time) (bucket, days int) {
	na, err := time.Parse(time.RFC3339, notAfter)
	if err != nil {
		return -1, 0
	}
	left := na.Sub(now)
	days = int(left.Hours() / 24)
	bucket = -1
	for _, th := range certThresholds {
		if days <= th {
			bucket = th
		}
	}
	if left <= 0 {
		bucket = 0
	} else if bucket == 0 {
		bucket = 3 // последний день — ещё не истёк
	}
	return bucket, days
}

// StartCertWatch — проверка сертификатов сайтов раз в сутки.
func (s *Server) StartCertWatch(ctx context.Context) {
	go func() {
		wait := certCheckFirst
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = certCheckEvery
			s.checkSiteCerts(ctx)
		}
	}()
}

func (s *Server) checkSiteCerts(ctx context.Context) {
	sites, err := s.db.ListSites(ctx)
	if err != nil {
		return
	}
	alerted := map[string]int{}
	if raw, ok, err := s.db.KVGet(ctx, certAlertedKV); err == nil && ok {
		_ = json.Unmarshal([]byte(raw), &alerted)
	}
	next := map[string]int{}
	for _, st := range sites {
		if st.Status != store.SiteOK || len(st.Domains) == 0 || ctx.Err() != nil {
			continue
		}
		t, err := s.siteTarget(ctx, st.HostID)
		if err != nil {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		chk := s.siteOutside(cctx, t, st.Domains, true)
		cancel()
		raw, _ := json.Marshal(chk)
		_ = s.db.SetSiteCheck(ctx, st.ID, raw)
		key := strconv.FormatInt(st.ID, 10)
		b, days := certBucket(chk.HTTPS.CertNotAfter, time.Now())
		if b < 0 {
			if chk.HTTPS.CertNotAfter == "" {
				// Не ответил — порог прежний, чтобы не оповестить снова.
				if prev, ok := alerted[key]; ok {
					next[key] = prev
				}
			}
			continue
		}
		next[key] = b
		if prev, ok := alerted[key]; ok && prev <= b {
			continue
		}
		s.alertCert(ctx, st, t, chk.HTTPS.CertNotAfter, days, b)
	}
	raw, _ := json.Marshal(next)
	_ = s.db.KVSet(ctx, certAlertedKV, string(raw))
}

func (s *Server) alertCert(ctx context.Context, st store.Site, t targetHost, notAfter string, days, bucket int) {
	host := store.Host{ID: t.ID, Name: t.Name, Addr: t.Addr}
	if t.ID != localHostID {
		if hh, err := s.db.HostByID(ctx, t.ID); err == nil {
			host = hh
		}
	}
	date := notAfter
	if len(date) > 10 {
		date = date[:10]
	}
	if bucket == 0 {
		s.hub.recordEventLink(ctx, host, store.EventCertExpiring, "critical", "", "hub.certExpired", st.Domains[0], date)
		return
	}
	sev := "warning"
	if bucket <= 3 {
		sev = "critical"
	}
	s.hub.recordEventLink(ctx, host, store.EventCertExpiring, sev, "", "hub.certExpiring", st.Domains[0], days, date)
}
