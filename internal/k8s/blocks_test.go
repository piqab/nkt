package k8s

import (
	"strings"
	"testing"
)

const blocksManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  replicas: 2
  template:
    spec:
      containers:
        - name: web
          image: nginx:1.27
          ports:
            - containerPort: 80
        - name: sidecar
          image: busybox

---
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  ports:
  - port: 80
    targetPort: 80
  - port: 443
  selector:
    app: web
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cfg
data:
  a: "1"
  b: |
    line1
    line2
`

func TestManifestBlocks(t *testing.T) {
	bl, err := ManifestBlocks(blocksManifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(bl) != 3 {
		t.Fatalf("объектов %d", len(bl))
	}
	d := bl[0]
	if d.Name != "Deployment shop/web" || d.StartLine != 1 || d.EndLine != 16 {
		t.Errorf("deployment: %q %d-%d", d.Name, d.StartLine, d.EndLine)
	}
	if len(d.Children) != 1 || d.Children[0].Name != "containers" || len(d.Children[0].Children) != 2 {
		t.Fatalf("containers: %+v", d.Children)
	}
	c := d.Children[0].Children
	if c[0].Name != "web" || c[0].StartLine != 11 || c[0].EndLine != 14 || c[1].Name != "sidecar" || c[1].EndLine != 16 || d.Children[0].Indent != 8 {
		t.Errorf("элементы: %+v %+v indent %d", c[0], c[1], d.Children[0].Indent)
	}
	s := bl[1]
	ports := s.Children[0]
	if s.StartLine != 19 || ports.Indent != 2 || len(ports.Children) != 2 || ports.Children[0].EndLine != 26 || ports.EndLine != 27 {
		t.Errorf("service: %d %+v", s.StartLine, ports)
	}
	cm := bl[2].Children[0]
	if len(cm.Children) != 2 || cm.Children[1].Name != "b" || !strings.Contains(cm.Children[1].Raw, "line2") {
		t.Errorf("configmap: %+v", cm)
	}
	if _, err := ManifestBlocks("a: ["); err == nil {
		t.Error("битый YAML принят")
	}
}

func TestValuesBlocks(t *testing.T) {
	bl, err := ManifestBlocks("replicaCount: 2\nimage:\n  repository: nginx\n  tag: \"1.27\"\n\nservice:\n  port: 80\n")
	if err != nil || len(bl) != 3 || bl[1].Name != "image" || bl[1].EndLine != 4 || bl[2].StartLine != 6 {
		t.Fatalf("%+v %v", bl, err)
	}
}
