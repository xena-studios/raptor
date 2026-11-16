package factorio

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDependencies(t *testing.T) {
	r := Release{}
	r.Info.Dependencies = []string{
		"base >= 2.1.22", "Krastorio2Assets >= 2.1.0", "? Aircraft >= 1.6.6", "(?) Flow Control >= 3.0.5",
		"! KS_Power", "~ ChangeInserterDropLane >= 1.3.0", "(?) space-age", "flib",
	}
	got := Dependencies(r)
	want := []Dependency{
		{"Krastorio2Assets", "required"},
		{"Aircraft", "optional"},
		{"Flow Control", "optional"},
		{"KS_Power", "incompatible"},
		{"ChangeInserterDropLane", "required"},
		{"flib", "required"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %v, want %v", i, got[i], want[i])
		}
	}
}

func TestFilesAndVersions(t *testing.T) {
	if m, v, ok := ParseFile("Krastorio2_2.1.4.zip"); !ok || m != "Krastorio2" || v != "2.1.4" {
		t.Errorf("ParseFile: %q %q %v", m, v, ok)
	}
	if m, _, ok := ParseFile("Flow Control_3.0.5.zip"); !ok || m != "Flow Control" {
		t.Errorf("a name with a space: %q", m)
	}
	if _, _, ok := ParseFile("mod-list.json"); ok {
		t.Error("not a mod")
	}
	for in, want := range map[string]string{"2.0.72": "2.0", "2.1": "2.1", "latest": "", "experimental": ""} {
		if got := Major(in); got != want {
			t.Errorf("Major(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mods" || r.URL.Query().Get("page_size") != "max" {
			http.NotFound(w, r)
			return
		}
		calls++
		_, _ = io.WriteString(w, `{"results":[
			{"name":"flib","title":"Factorio Library","downloads_count":5,"latest_release":{"file_name":"flib_0.16.2.zip","info_json":{"factorio_version":"2.0"}}},
			{"name":"Krastorio2","title":"Krastorio 2","summary":"overhaul","downloads_count":90,"latest_release":{"file_name":"Krastorio2_2.1.4.zip","info_json":{"factorio_version":"2.1"}}},
			{"name":"old","title":"Old","downloads_count":99,"latest_release":{"file_name":"old_1.0.0.zip","info_json":{"factorio_version":"1.1"}}},
			{"name":"gone","title":"No releases","downloads_count":1000}]}`)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, UserAgent: "test"}
	ctx := context.Background()
	hits, total, err := c.Search(ctx, "", "", 0)
	if err != nil || total != 3 || hits[0].Name != "old" || hits[1].Name != "Krastorio2" {
		t.Fatalf("all, most downloaded first: %v %d %v", hits, total, err)
	}
	if hits, _, _ := c.Search(ctx, "OVERHAUL", "2.1", 0); len(hits) != 1 || hits[0].Name != "Krastorio2" {
		t.Errorf("by summary and version: %v", hits)
	}
	if hits, _, _ := c.Search(ctx, "", "2.0", 0); len(hits) != 1 || hits[0].Name != "flib" {
		t.Errorf("for 2.0: %v", hits)
	}
	if calls != 1 {
		t.Errorf("the list was fetched %d times; it's kept for an hour", calls)
	}
}
