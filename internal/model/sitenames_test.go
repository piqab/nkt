package model

import (
	"reflect"
	"testing"
)

func TestAttachSiteNames(t *testing.T) {
	t.Run("groups names by file, in first-seen order", func(t *testing.T) {
		files := []ManagedFile{{Path: "/etc/nginx/sites-enabled/a.conf"}, {Path: "/etc/nginx/sites-enabled/b.conf"}}
		endpoints := []Endpoint{
			{File: "/etc/nginx/sites-enabled/a.conf", Names: []string{"a.example.com", "www.a.example.com"}},
			{File: "/etc/nginx/sites-enabled/b.conf", Names: []string{"b.example.com"}},
		}
		AttachSiteNames(files, endpoints)

		wantA := []SiteName{{Name: "a.example.com"}, {Name: "www.a.example.com"}}
		if !reflect.DeepEqual(files[0].Sites, wantA) {
			t.Errorf("files[0].Sites = %+v, want %+v", files[0].Sites, wantA)
		}
		wantB := []SiteName{{Name: "b.example.com"}}
		if !reflect.DeepEqual(files[1].Sites, wantB) {
			t.Errorf("files[1].Sites = %+v, want %+v", files[1].Sites, wantB)
		}
	})

	t.Run("deduplicates a name repeated across endpoints in the same file", func(t *testing.T) {
		files := []ManagedFile{{Path: "/etc/nginx/sites-enabled/a.conf"}}
		endpoints := []Endpoint{
			{File: "/etc/nginx/sites-enabled/a.conf", Names: []string{"a.example.com"}},
			{File: "/etc/nginx/sites-enabled/a.conf", Names: []string{"a.example.com"}}, // e.g. HTTP + HTTPS server blocks
		}
		AttachSiteNames(files, endpoints)

		want := []SiteName{{Name: "a.example.com"}}
		if !reflect.DeepEqual(files[0].Sites, want) {
			t.Errorf("Sites = %+v, want deduplicated %+v", files[0].Sites, want)
		}
	})

	t.Run("IDN name gets its Unicode form alongside the ASCII one", func(t *testing.T) {
		files := []ManagedFile{{Path: "/etc/nginx/sites-enabled/idn.conf"}}
		endpoints := []Endpoint{
			{File: "/etc/nginx/sites-enabled/idn.conf", Names: []string{"xn--80akhbyknj4f.xn--p1ai"}},
		}
		AttachSiteNames(files, endpoints)

		if len(files[0].Sites) != 1 {
			t.Fatalf("Sites = %+v, want exactly one entry", files[0].Sites)
		}
		got := files[0].Sites[0]
		if got.Name != "xn--80akhbyknj4f.xn--p1ai" || got.NameUnicode != "испытание.рф" {
			t.Errorf("Sites[0] = %+v, want Name=xn--80akhbyknj4f.xn--p1ai NameUnicode=испытание.рф", got)
		}
	})

	t.Run("plain ASCII name has no NameUnicode footnote", func(t *testing.T) {
		files := []ManagedFile{{Path: "/etc/nginx/sites-enabled/a.conf"}}
		endpoints := []Endpoint{{File: "/etc/nginx/sites-enabled/a.conf", Names: []string{"example.com"}}}
		AttachSiteNames(files, endpoints)

		if got := files[0].Sites[0].NameUnicode; got != "" {
			t.Errorf("NameUnicode = %q for a plain ASCII name, want empty", got)
		}
	})

	t.Run("file with no matching endpoint is left untouched", func(t *testing.T) {
		files := []ManagedFile{{Path: "/etc/nginx/sites-enabled/unmatched.conf"}}
		endpoints := []Endpoint{{File: "/etc/nginx/sites-enabled/other.conf", Names: []string{"other.example.com"}}}
		AttachSiteNames(files, endpoints)

		if files[0].Sites != nil {
			t.Errorf("Sites = %+v, want nil for a file with no matching endpoint", files[0].Sites)
		}
	})

	t.Run("no endpoints at all: no-op, does not panic", func(t *testing.T) {
		files := []ManagedFile{{Path: "/etc/nginx/sites-enabled/a.conf"}}
		AttachSiteNames(files, nil)
		if files[0].Sites != nil {
			t.Errorf("Sites = %+v, want nil", files[0].Sites)
		}
	})

	t.Run("endpoint with empty File is ignored, not grouped under an empty-path entry", func(t *testing.T) {
		files := []ManagedFile{{Path: ""}, {Path: "/etc/nginx/sites-enabled/a.conf"}}
		endpoints := []Endpoint{{File: "", Names: []string{"docker-published-port"}}}
		AttachSiteNames(files, endpoints)

		if files[0].Sites != nil {
			t.Errorf("Sites for the empty-Path file = %+v, want nil", files[0].Sites)
		}
	})
}
