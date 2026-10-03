package deploy

import (
	"slices"
	"testing"
)

func TestScanCompose(t *testing.T) {
	text := `services:
  web:
    image: kennethreitz/httpbin
    environment:
      SECRET_KEY: ${APP_SECRET}
      LOG_LEVEL: ${LOG_LEVEL:-info}
      URL: ${PUBLIC_URL:?set it}
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf:ro
      - data:/data
      - type: bind
        source: ../shared/init.sh
        target: /init.sh
    ports:
      - "127.0.0.1:8080:80"
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    expose: ["5432"]
  worker:
    build: ./worker
  admin:
    image: adminer:latest
    ports:
      - target: 8080
        published: 8081
volumes:
  data:
`
	ports := map[string][]int{"kennethreitz/httpbin": {80}, "postgres:17-alpine": {5432}}
	sc, err := ScanCompose(text, "deploy/docker-compose.yml", func(img string) []int { return ports[img] })
	if err != nil {
		t.Fatal(err)
	}
	if sc.Web != "web" || sc.WebPort != 80 {
		t.Errorf("web %q %d", sc.Web, sc.WebPort)
	}
	by := map[string]ScanService{}
	for _, s := range sc.Services {
		by[s.Name] = s
	}
	if !by["db"].DB || by["web"].DB || !by["worker"].BuildOnly || !by["admin"].Unpinned || by["db"].Unpinned {
		t.Errorf("services %+v", sc.Services)
	}
	if !slices.Equal(by["web"].Published, []string{"127.0.0.1:8080:80"}) || !slices.Equal(by["admin"].Published, []string{"8081:8080"}) || !slices.Equal(by["admin"].Ports, []int{8080}) {
		t.Errorf("ports %+v / %+v", by["web"], by["admin"])
	}
	if !slices.Equal(sc.Files, []string{"deploy/nginx.conf", "shared/init.sh"}) {
		t.Errorf("files %v", sc.Files)
	}
	vars := map[string]ScanVar{}
	for _, v := range sc.Vars {
		vars[v.Name] = v
	}
	if !vars["APP_SECRET"].Secret || !vars["POSTGRES_PASSWORD"].Secret || vars["LOG_LEVEL"].Default != "info" || !vars["PUBLIC_URL"].Required || vars["LOG_LEVEL"].Secret {
		t.Errorf("vars %+v", sc.Vars)
	}
	if sc.WebPortOf("admin") != 8080 {
		t.Errorf("admin port %d", sc.WebPortOf("admin"))
	}
	// Без портов образа (registry недоступен) — из compose; база сайтом не
	// становится даже с портом.
	sc2, _ := ScanCompose("services:\n  db:\n    image: mariadb:11\n    ports: [\"3306:3306\"]\n  app:\n    image: ghcr.io/x/app:1\n    expose: [\"3000\"]\n", "docker-compose.yml", nil)
	if sc2.Web != "app" || sc2.WebPort != 3000 {
		t.Errorf("fallback web %q %d", sc2.Web, sc2.WebPort)
	}
	if _, err := ScanCompose("services: [", "x.yml", nil); err == nil {
		t.Error("bad yaml accepted")
	}
}
