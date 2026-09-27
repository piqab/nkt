package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// SameOrigin — защита от подделки запросов (CSRF): изменяющий запрос
// (не GET/HEAD/OPTIONS) принимается, только если браузер говорит, что он
// со своего же адреса. Cookie сессии SameSite=Lax браузер шлёт и на
// запросы с соседнего порта того же хоста (для cookie это «тот же
// сайт»), — а на соседнем порту живут пробросы портов с чужими
// приложениями; их страницы не должны мочь что-то изменить в nkt.
//
//   - Sec-Fetch-Site (его ставят все современные браузеры): годится
//     «same-origin» и «none» (адрес набран руками, закладка);
//   - без него — Origin должен совпадать с Host запроса;
//   - без обоих (curl, запросы хаба к хосту) — пропускается: это не
//     браузер, и cookie у такого клиента своя.
//
// allowed — дополнительно разрешённые источники (NKT_CORS_ORIGINS).
// exempt — пути, куда доступ не по сессии (вебхуки, проброс по токену).
func SameOrigin(allowed []string, exempt func(path string) bool) func(http.Handler) http.Handler {
	ok := map[string]bool{}
	for _, o := range allowed {
		ok[strings.TrimRight(o, "/")] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if exempt != nil && exempt(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin != "" && ok[strings.TrimRight(origin, "/")] {
				next.ServeHTTP(w, r)
				return
			}
			if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" {
				if sfs != "same-origin" && sfs != "none" {
					http.Error(w, `{"error":"cross-origin request refused"}`, http.StatusForbidden)
					return
				}
			} else if origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, `{"error":"cross-origin request refused"}`, http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
