package catalog_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piqab/nkt/internal/catalog"
	"github.com/piqab/nkt/internal/catalog/catalogtest"
)

type entry struct {
	name string
	body string
	link bool
}

func tgz(t *testing.T, es []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range es {
		h := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link {
			h = &tar.Header{Name: e.name, Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}
		}
		_ = tw.WriteHeader(h)
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// SampleArchive — examples/_samples/url-check как архив .tar.gz (с верхней
// папкой, как его делает «tar czf … -C examples/_samples url-check»).
func sampleArchive(t *testing.T) []byte {
	t.Helper()
	dir := filepath.Join(catalogtest.ExamplesDir(), "_samples", "url-check")
	var es []entry
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && !strings.Contains(p, "__pycache__") {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(filepath.Dir(dir), p)
			es = append(es, entry{name: filepath.ToSlash(rel), body: string(b)})
		}
		return nil
	})
	return tgz(t, es)
}

// Пример url-check: архив читается, верхняя папка снимается, тест —
// пропускается, проверка без замечаний, ключ «local:url-check».
func TestUploadSample(t *testing.T) {
	files, skipped, err := catalog.ReadUpload(sampleArchive(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["deploy/nkt-plugin.py"]; !ok {
		t.Fatalf("files: %v", keys(files))
	}
	up, err := catalog.CheckUpload(files, skipped)
	if err != nil || len(up.Problems) > 0 {
		t.Fatalf("check: %v %v", err, up.Problems)
	}
	if up.Plugin.Key != "local:url-check" || up.Plugin.Version != "1.0.0" || len(up.Code) < 1000 || up.Plugin.Readme["en"] != "README.en.md" {
		t.Fatalf("upload: %+v", up.Plugin)
	}
	if _, ok := up.Plugin.Files["deploy/test_plugin.py"]; ok {
		t.Fatal("the test is kept")
	}
}

// Опасные архивы — отказ; посторонние файлы — пропуск с перечнем.
func TestUploadRejects(t *testing.T) {
	for name, data := range map[string][]byte{
		"dotdot":   tgz(t, []entry{{name: "p/../../etc/cron.d/x", body: "x"}}),
		"absolute": tgz(t, []entry{{name: "/etc/passwd", body: "x"}}),
		"symlink":  tgz(t, []entry{{name: "p/deploy/nkt-plugin.py", link: true}}),
		"big":      tgz(t, []entry{{name: "p/deploy/nkt-plugin.py", body: strings.Repeat("x", catalog.UploadMaxFile+1)}}),
		"text":     []byte("not an archive"),
	} {
		if _, _, err := catalog.ReadUpload(data); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	files, skipped, err := catalog.ReadUpload(tgz(t, []entry{{name: "p/deploy/nkt-plugin.json", body: "{}"}, {name: "p/deploy/__pycache__/x.pyc", body: "x"},
		{name: "p/run.sh", body: "x"}}))
	if err != nil || len(files) != 1 || strings.Join(skipped, ",") != "deploy/__pycache__/x.pyc,run.sh" {
		t.Fatalf("skip: %v %v %v", keys(files), skipped, err)
	}
	// zip тоже читается.
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("url-check/deploy/nkt-plugin.json")
	_, _ = w.Write([]byte(`{"name":"x"}`))
	_ = zw.Close()
	if files, _, err := catalog.ReadUpload(zb.Bytes()); err != nil || files["deploy/nkt-plugin.json"] == nil {
		t.Fatalf("zip: %v %v", keys(files), err)
	}
	// Без shell=True и прочего — плохой код не проходит.
	files, skipped, _ = catalog.ReadUpload(sampleArchive(t))
	files["deploy/nkt-plugin.py"] = append(files["deploy/nkt-plugin.py"], []byte("\nimport os\nos.system(cmd)\n")...)
	if up, _ := catalog.CheckUpload(files, skipped); len(up.Problems) == 0 {
		t.Fatal("os.system accepted")
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
