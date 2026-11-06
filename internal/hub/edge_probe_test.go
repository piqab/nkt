package hub

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/hashicorp/yamux"

	"github.com/piqab/nkt/internal/edge"
)

// outsideEdgeFixture — хаб, у которого есть edge с ролью probe, соединённый
// по туннелю: хаб открывает поток, «edge» отвечает итогами из respond, а
// о чём его спрашивали, остаётся в asked.
func outsideEdgeFixture(t *testing.T, respond func(edge.ProbeRequest) edge.ProbeResponse) (*Server, *[]edge.ProbeCheck, int64) {
	t.Helper()
	srv, _, _ := localFixtureHub(t)
	ctx := context.Background()
	hubSide, edgeSide := net.Pipe()
	hubSess, err := yamux.Client(hubSide, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	edgeSess, err := yamux.Server(edgeSide, yamux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hubSess.Close(); edgeSess.Close() })
	asked := new([]edge.ProbeCheck)
	mux := http.NewServeMux()
	mux.HandleFunc(edge.ProbePath, func(w http.ResponseWriter, r *http.Request) {
		var req edge.ProbeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		*asked = req.Checks
		_ = json.NewEncoder(w).Encode(respond(req))
	})
	go func() { _ = (&http.Server{Handler: mux}).Serve(edgeSess) }()

	st, err := srv.putEdge(ctx, EdgeSettings{Enabled: true, Address: "edge-a:8444", Domain: "probe.example.com", Roles: []string{EdgeRoleProbe}})
	if err != nil {
		t.Fatal(err)
	}
	srv.edge = &edgeHub{clients: map[int64]*edgeClient{st.ID: {connected: true, sess: hubSess, cancel: func() {}}}, reload: make(chan struct{}, 1)}
	return srv, asked, st.ID
}

// Ответы edge для проверок снаружи: адреса имени, состояние портов, код
// https (withPath — вернуть и путь, как edge новой версии).
func probeAnswers(status int, withPath bool) func(edge.ProbeRequest) edge.ProbeResponse {
	return func(req edge.ProbeRequest) edge.ProbeResponse {
		out := edge.ProbeResponse{}
		for _, c := range req.Checks {
			res := edge.ProbeResult{ProbeCheck: c}
			switch c.Type {
			case "dns":
				res.IPs = []string{"198.51.100.20"}
			case "tcp":
				res.State = map[int]string{80: "open", 443: "timeout"}[c.Port]
			case "https":
				res.Status = status
				if !withPath {
					// Edge прежней версии: поля path он не знает и в ответе
					// его не возвращает.
					res.Path = ""
				}
			}
			out.Results = append(out.Results, res)
		}
		return out
	}
}

// Проверка сайта «снаружи» идёт с edge с ролью probe, если он подключён:
// хаб открывает поток, edge отвечает итогами.
func TestSiteOutsideViaEdge(t *testing.T) {
	srv, asked, edgeID := outsideEdgeFixture(t, probeAnswers(0, true))
	ctx := context.Background()

	chk := srv.siteOutside(ctx, targetHost{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"}, []string{"shop.example.com"}, false, "")
	if chk.Via != "probe.example.com" || chk.Ports["80"] != "open" || chk.Ports["443"] != "timeout" {
		t.Fatalf("check: %+v", chk)
	}
	if len(chk.DNS) != 1 || len(chk.DNS[0].DomainIPs) != 1 || chk.DNS[0].DomainIPs[0] != "198.51.100.20" {
		t.Fatalf("dns: %+v", chk.DNS)
	}
	if len(*asked) != 3 {
		t.Fatalf("asked: %+v", *asked)
	}
	// Edge без роли probe — проверка с хаба.
	_, _ = srv.putEdge(ctx, EdgeSettings{ID: edgeID, Enabled: true, Address: "edge-a:8444", Domain: "probe.example.com", Roles: []string{EdgeRoleHooks}})
	if _, _, ok := srv.probeEdge(ctx, 0); ok {
		t.Fatal("hooks-only edge used for probing")
	}
}

// Названный путь доходит до edge и возвращается в итоге: 401 на «/ui» —
// интерфейс жив и просит вход, это успех; 404 — путь не открылся, провал.
// Edge прежней версии поля не знает и отвечает без пути — значит проверен
// корень, и судить надо по правилу корня (любой код кроме 5xx), а не
// объявлять выкладку упавшей из-за 404 в корне.
func TestSiteOutsideHTTPSPath(t *testing.T) {
	ctx := context.Background()
	target := targetHost{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"}
	for _, c := range []struct {
		name    string
		echo    bool
		status  int
		wantOK  bool
		wantErr string
	}{
		{"401 на /ui — путь просит вход", true, 401, true, ""},
		{"302 на /ui — путь редиректит", true, 302, true, ""},
		{"404 на /ui — путь не открылся", true, 404, false, "404"},
		{"502 на /ui", true, 502, false, "502"},
		{"edge прежней версии, 404 в корне — это норма", false, 404, true, ""},
		{"старый edge, 502 в корне", false, 502, false, "502"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, asked, _ := outsideEdgeFixture(t, probeAnswers(c.status, c.echo))
			chk := srv.siteOutside(ctx, target, []string{"shop.example.com"}, true, "/ui")
			https := 0
			for _, a := range *asked {
				if a.Type == "https" {
					https++
					if a.Path != "/ui" {
						t.Fatalf("путь не доехал до edge: %+v", a)
					}
				}
			}
			if len(*asked) != 4 || https != 1 {
				t.Fatalf("checks: %+v", *asked)
			}
			wantPath := "/ui"
			if !c.echo {
				wantPath = ""
			}
			if chk.HTTPS.Path != wantPath || chk.HTTPS.Status != c.status || chk.HTTPS.OK != c.wantOK || chk.HTTPS.Error != c.wantErr {
				t.Fatalf("https: %+v, want path=%q ok=%v err=%q", chk.HTTPS, wantPath, c.wantOK, c.wantErr)
			}
		})
	}
}
