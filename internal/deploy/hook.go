package deploy

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/piqab/nkt/internal/msgs"
)

// Вебхук конвейера. Принимается только подписанный запрос:
//
//	GitHub          X-Hub-Signature-256: sha256=<HMAC-SHA256(секрет, тело)>
//	Gitea/Forgejo   X-Gitea-Signature / X-Forgejo-Signature: <HMAC hex>
//	GitLab          X-Gitlab-Token: <секрет>
//	nkt (из CI)     X-NKT-Timestamp: <unix>, X-NKT-Signature: <HMAC(секрет, "ts.тело")>
//
// У каждой доставки — свой идентификатор (заголовок сервиса или сама
// подпись): повтор той же доставки отвергает хаб (store.RememberDelivery).
// У подписи nkt ещё и срок: отметка времени не старше 5 минут.

// MaxHookBody — предел тела вебхука.
const MaxHookBody = 1 << 20

// hookSkew — насколько отметка времени nkt может расходиться с часами хаба.
const hookSkew = 5 * time.Minute

// HookEvent — что пришло.
type HookEvent struct {
	Provider string
	Delivery string
	Ping     bool
	// Ref — полное имя ссылки (refs/heads/main, refs/tags/v1.2.3).
	Ref string
	// Commit — коммит после push (пусто — не указан).
	Commit string
	// Tag — тег образа из запроса nkt (для {{nkt.tag}}).
	Tag string
	// Deleted — ветку или тег удалили.
	Deleted bool
}

// VerifyHook проверяет подпись и разбирает событие.
func VerifyHook(h http.Header, body []byte, secret string, now time.Time) (HookEvent, error) {
	ev := HookEvent{}
	mac := func(data []byte) []byte {
		m := hmac.New(sha256.New, []byte(secret))
		m.Write(data)
		return m.Sum(nil)
	}
	eqHex := func(got string, want []byte) bool {
		b, err := hex.DecodeString(strings.TrimSpace(got))
		return err == nil && hmac.Equal(b, want)
	}
	switch {
	case h.Get("X-Hub-Signature-256") != "":
		sig, ok := strings.CutPrefix(h.Get("X-Hub-Signature-256"), "sha256=")
		if !ok || !eqHex(sig, mac(body)) {
			return ev, msgs.Errorf("deploy.hookBadSignature")
		}
		ev.Provider, ev.Delivery = "github", h.Get("X-GitHub-Delivery")
		ev.Ping = h.Get("X-GitHub-Event") == "ping"
	case h.Get("X-Gitea-Signature") != "" || h.Get("X-Forgejo-Signature") != "":
		sig := h.Get("X-Gitea-Signature")
		if sig == "" {
			sig = h.Get("X-Forgejo-Signature")
		}
		if !eqHex(sig, mac(body)) {
			return ev, msgs.Errorf("deploy.hookBadSignature")
		}
		ev.Provider, ev.Delivery = "gitea", h.Get("X-Gitea-Delivery")
		if ev.Delivery == "" {
			ev.Delivery = h.Get("X-Forgejo-Delivery")
		}
	case h.Get("X-Gitlab-Token") != "":
		if subtle.ConstantTimeCompare([]byte(h.Get("X-Gitlab-Token")), []byte(secret)) != 1 {
			return ev, msgs.Errorf("deploy.hookBadSignature")
		}
		ev.Provider, ev.Delivery = "gitlab", h.Get("X-Gitlab-Event-UUID")
	case h.Get("X-NKT-Signature") != "":
		ts, err := strconv.ParseInt(h.Get("X-NKT-Timestamp"), 10, 64)
		if err != nil {
			return ev, msgs.Errorf("deploy.hookBadSignature")
		}
		if d := now.Sub(time.Unix(ts, 0)); d > hookSkew || d < -hookSkew {
			return ev, msgs.Errorf("deploy.hookExpired")
		}
		signed := append([]byte(strconv.FormatInt(ts, 10)+"."), body...)
		if !eqHex(h.Get("X-NKT-Signature"), mac(signed)) {
			return ev, msgs.Errorf("deploy.hookBadSignature")
		}
		ev.Provider, ev.Delivery = "nkt", "nkt:"+strings.ToLower(strings.TrimSpace(h.Get("X-NKT-Signature")))
	default:
		return ev, msgs.Errorf("deploy.hookUnsigned")
	}
	if ev.Delivery == "" {
		// Без идентификатора доставки — сама подпись тела: повтор того же
		// запроса совпадёт.
		sum := sha256.Sum256(append([]byte(ev.Provider+":"), body...))
		ev.Delivery = ev.Provider + ":" + hex.EncodeToString(sum[:])
	} else if ev.Provider != "nkt" {
		ev.Delivery = ev.Provider + ":" + ev.Delivery
	}
	if ev.Ping {
		return ev, nil
	}
	var p struct {
		Ref         string `json:"ref"`
		After       string `json:"after"`
		CheckoutSHA string `json:"checkout_sha"`
		Commit      string `json:"commit"`
		Tag         string `json:"tag"`
		Deleted     bool   `json:"deleted"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &p); err != nil {
			return ev, msgs.Errorf("deploy.hookBadBody")
		}
	}
	ev.Ref, ev.Tag = p.Ref, p.Tag
	for _, c := range []string{p.Commit, p.After, p.CheckoutSHA} {
		if ValidSHA(c) {
			ev.Commit = c
			break
		}
	}
	ev.Deleted = p.Deleted || p.After == strings.Repeat("0", 40)
	if ev.Tag != "" && !ValidRef(ev.Tag) {
		return ev, msgs.Errorf("deploy.specBad", "tag", ev.Tag)
	}
	return ev, nil
}

// Decision — что делать с событием для описания конвейера.
type Decision struct {
	Deploy bool
	Ref    string // ветка или тег репозитория
	Commit string
	Tag    string
	// Reason — ключ msgs, почему пропущено.
	Reason string
}

// Decide сопоставляет событие с описанием: push в ветку ref — выкладка
// её вершины; тег по шаблону tags — выкладка тега; запрос nkt с одним
// тегом образа — выкладка ветки с этим тегом.
func Decide(spec Spec, ev HookEvent) Decision {
	if ev.Deleted {
		return Decision{Reason: "deploy.hookDeleted"}
	}
	switch {
	case strings.HasPrefix(ev.Ref, "refs/heads/"):
		branch := strings.TrimPrefix(ev.Ref, "refs/heads/")
		if spec.Ref == "" || branch != spec.Ref {
			return Decision{Reason: "deploy.hookOtherBranch"}
		}
		return Decision{Deploy: true, Ref: branch, Commit: ev.Commit, Tag: ev.Tag}
	case strings.HasPrefix(ev.Ref, "refs/tags/"):
		tag := strings.TrimPrefix(ev.Ref, "refs/tags/")
		if !MatchTag(spec.Tags, tag) || !ValidRef(tag) {
			return Decision{Reason: "deploy.hookOtherTag"}
		}
		return Decision{Deploy: true, Ref: tag, Commit: ev.Commit, Tag: tag}
	case ev.Ref == "" && ev.Tag != "" && spec.Ref != "":
		return Decision{Deploy: true, Ref: spec.Ref, Tag: ev.Tag}
	}
	return Decision{Reason: "deploy.hookNoRef"}
}
