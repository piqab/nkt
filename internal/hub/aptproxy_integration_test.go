package hub

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piqab/nkt/internal/config"
	"github.com/piqab/nkt/internal/secretbox"
	"github.com/piqab/nkt/internal/store"
)

// Кэш пакетов доезжает до хоста обратным пробросом по настоящему sshd:
// хост видит прокси на 127.0.0.1:<порт>, первый .deb уходит наверх,
// второй — из кэша.
func TestAptProxyReverseForward(t *testing.T) {
	sshAddr, sshPort, clientKeyPEM := startTestSSHD(t)
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Write([]byte("deb-bytes"))
	}))
	defer upstream.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, _ := secretbox.GenerateKey()
	me, _ := osuser.Current()
	secretEnc, _ := secretbox.Encrypt(key, clientKeyPEM)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hostID, err := db.CreateHost(ctx, "h", sshAddr, sshPort, me.Username, store.HostAuthKey, secretEnc)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.SetHostStatus(ctx, hostID, store.HostStatusOnline, "")
	_ = db.SetHostAptViaHub(ctx, hostID, true)

	// Свободный порт под проброс.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cfg := &config.Config{DataDir: t.TempDir(), HubAptCacheMaxGB: 1, HubAptCachePort: port}
	m := NewManager(cfg, db, key, "test", slog.New(slog.DiscardHandler))

	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	go m.runAptProxy(pctx, hostID)
	deadline := time.Now().Add(10 * time.Second)
	for !m.AptProxyConnected(hostID) {
		if time.Now().After(deadline) {
			t.Fatal("проброс не поднялся")
		}
		time.Sleep(100 * time.Millisecond)
	}

	proxyURL, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	for i := 0; i < 2; i++ {
		resp, err := client.Get(upstream.URL + "/debian/pool/main/x/x_1.deb")
		if err != nil {
			t.Fatalf("через проброс: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "deb-bytes" {
			t.Fatalf("тело: %q", body)
		}
	}
	if upstreamHits != 1 {
		t.Errorf("наверх ушло %d запросов, ожидался 1", upstreamHits)
	}
	if st := m.AptCache().Stats(); st.Hits != 1 || st.Entries != 1 {
		t.Errorf("stats: %+v", st)
	}
	pcancel()
	deadline = time.Now().Add(5 * time.Second)
	for m.AptProxyConnected(hostID) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if m.AptProxyConnected(hostID) {
		t.Error("после отмены проброс всё ещё числится живым")
	}
}

func TestAptProxyDetectScript(t *testing.T) {
	s := aptProxyDetectScript(3142)
	for _, want := range []string{"#!/bin/bash", "/dev/tcp/127.0.0.1/3142", "http://127.0.0.1:3142", "DIRECT"} {
		if !strings.Contains(s, want) {
			t.Errorf("нет %q", want)
		}
	}
}
