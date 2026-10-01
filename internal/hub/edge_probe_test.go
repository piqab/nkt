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

// Проверка сайта «снаружи» идёт с edge с ролью probe, если он подключён:
// хаб открывает поток, edge отвечает итогами.
func TestSiteOutsideViaEdge(t *testing.T) {
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
	defer hubSess.Close()
	defer edgeSess.Close()
	var asked []edge.ProbeCheck
	mux := http.NewServeMux()
	mux.HandleFunc(edge.ProbePath, func(w http.ResponseWriter, r *http.Request) {
		var req edge.ProbeRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		asked = req.Checks
		out := edge.ProbeResponse{}
		for _, c := range req.Checks {
			res := edge.ProbeResult{ProbeCheck: c}
			switch c.Type {
			case "dns":
				res.IPs = []string{"198.51.100.20"}
			case "tcp":
				res.State = map[int]string{80: "open", 443: "timeout"}[c.Port]
			}
			out.Results = append(out.Results, res)
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	go func() { _ = (&http.Server{Handler: mux}).Serve(edgeSess) }()

	st, err := srv.putEdge(ctx, EdgeSettings{Enabled: true, Address: "vps:8444", Domain: "probe.example.com", Roles: []string{EdgeRoleProbe}})
	if err != nil {
		t.Fatal(err)
	}
	srv.edge = &edgeHub{clients: map[int64]*edgeClient{st.ID: {connected: true, sess: hubSess, cancel: func() {}}}, reload: make(chan struct{}, 1)}

	chk := srv.siteOutside(ctx, targetHost{ID: localHostID, Name: "localhost", Addr: "127.0.0.1"}, []string{"shop.example.com"}, false)
	if chk.Via != "probe.example.com" || chk.Ports["80"] != "open" || chk.Ports["443"] != "timeout" {
		t.Fatalf("check: %+v", chk)
	}
	if len(chk.DNS) != 1 || len(chk.DNS[0].DomainIPs) != 1 || chk.DNS[0].DomainIPs[0] != "198.51.100.20" {
		t.Fatalf("dns: %+v", chk.DNS)
	}
	if len(asked) != 3 {
		t.Fatalf("asked: %+v", asked)
	}
	// Edge без роли probe — проверка с хаба.
	_, _ = srv.putEdge(ctx, EdgeSettings{ID: st.ID, Enabled: true, Address: "vps:8444", Domain: "probe.example.com", Roles: []string{EdgeRoleHooks}})
	if _, _, ok := srv.probeEdge(ctx, 0); ok {
		t.Fatal("hooks-only edge used for probing")
	}
}
