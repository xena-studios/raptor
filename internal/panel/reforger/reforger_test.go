package reforger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const rhs = `{"id":"595F2BF2F44836FB","name":"RHS - Status Quo","type":"addon","summary":"RHS on Arma Reforger",
"private":false,"blocked":false,"currentVersionNumber":"0.17.5281","currentVersionSize":204512992,
"updatedAt":"2026-11-05T18:33:01.000Z","author":{"username":"Red Hammer Studios"},
"previews":[{"url":"https://ar-gcp-cdn.bistudio.com/image/a/1.jpg","thumbnails":{"image/jpeg":[
 {"url":"https://ar-gcp-cdn.bistudio.com/image/b/2.jpg","width":576},
 {"url":"https://ar-gcp-cdn.bistudio.com/image/c/3.jpg","width":288},
 {"url":"https://ar-gcp-cdn.bistudio.com/image/d/4.jpg","width":144}]}}],
"dependencies":[{"version":"0.17.5281","asset":{"id":"1337C0DE5DABBEEF","name":"RHS - Content Pack 01"}}],
"scenarios":[{"name":"Conflict: Everon","gameId":"{ABC}Missions/RHS.conf"}]}`

func html(props string) string {
	return `<html><head></head><body><div id="__next"></div><script id="__NEXT_DATA__" type="application/json">` +
		`{"props":{"pageProps":` + props + `},"page":"/workshop/[id]"}</script></body></html>`
}

func server(t *testing.T, hits *atomic.Int32) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		switch {
		case r.URL.Path == "/workshop/595F2BF2F44836FB":
			_, _ = fmt.Fprint(w, html(`{"pathId":"595F2BF2F44836FB","asset":`+rhs+`}`))
		case r.URL.Path == "/workshop/AAAAAAAAAAAAAAAA":
			_, _ = fmt.Fprint(w, html(`{"asset":{"id":"AAAAAAAAAAAAAAAA","name":"Hidden","private":true}}`))
		case r.URL.Path == "/workshop/BBBBBBBBBBBBBBBB":
			_, _ = fmt.Fprint(w, "<html>a redesign without page data</html>")
		case r.URL.Path == "/workshop" && r.URL.Query().Get("search") == "rhs":
			_, _ = fmt.Fprint(w, html(`{"search":"rhs","page":1,"assets":{"count":2410,"rows":[`+rhs+`,
				{"id":"not-an-id","name":"x"},{"id":"CCCCCCCCCCCCCCCC","name":"Banned","blocked":true}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL, UserAgent: "test"}
}

func TestGet(t *testing.T) {
	var hits atomic.Int32
	c := server(t, &hits)
	m, err := c.Get(context.Background(), "595F2BF2F44836FB")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "RHS - Status Quo" || m.Author != "Red Hammer Studios" || m.Version != "0.17.5281" || m.Size != 204512992 {
		t.Errorf("mod %+v", m)
	}
	if m.Preview != "https://ar-gcp-cdn.bistudio.com/image/c/3.jpg" {
		t.Errorf("preview %q, want the 288 thumbnail", m.Preview)
	}
	if len(m.Dependencies) != 1 || m.Dependencies[0] != (Ref{"1337C0DE5DABBEEF", "RHS - Content Pack 01"}) {
		t.Errorf("dependencies %+v", m.Dependencies)
	}
	if len(m.Scenarios) != 1 || m.Scenarios[0].ID != "{ABC}Missions/RHS.conf" {
		t.Errorf("scenarios %+v", m.Scenarios)
	}
	if m.Updated.IsZero() {
		t.Error("no updated time")
	}
	// Cached.
	if _, err := c.Get(context.Background(), "595F2BF2F44836FB"); err != nil || hits.Load() != 1 {
		t.Errorf("second get: %v, %d requests", err, hits.Load())
	}
}

func TestGetMissing(t *testing.T) {
	c := server(t, nil)
	for _, id := range []string{"0123456789ABCDEF", "AAAAAAAAAAAAAAAA", "not-hex", "595f2bf2f44836fb"} {
		if _, err := c.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want not found", id, err)
		}
	}
	if _, err := c.Get(context.Background(), "BBBBBBBBBBBBBBBB"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a page without data: %v, want unavailable", err)
	}
}

func TestLookup(t *testing.T) {
	c := server(t, nil)
	got, err := c.Lookup(context.Background(), []string{"595F2BF2F44836FB", "0123456789ABCDEF", "595F2BF2F44836FB"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["595F2BF2F44836FB"].Name != "RHS - Status Quo" {
		t.Errorf("lookup %+v", got)
	}
	// Only unanswered: the Workshop is down.
	if _, err := c.Lookup(context.Background(), []string{"BBBBBBBBBBBBBBBB"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("down: %v", err)
	}
}

func TestSearch(t *testing.T) {
	c := server(t, nil)
	mods, total, err := c.Search(context.Background(), "rhs", 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2410 || len(mods) != 1 || mods[0].ID != "595F2BF2F44836FB" {
		t.Errorf("search %d %+v", total, mods)
	}
}

func TestPreviewHost(t *testing.T) {
	var a asset
	if a.preview() != "" {
		t.Error("no previews: want none")
	}
	if err := json.Unmarshal([]byte(`{"previews":[{"url":"https://evil.example/x.jpg"}]}`), &a); err != nil {
		t.Fatal(err)
	}
	if p := a.preview(); p != "" {
		t.Errorf("preview elsewhere: %q", p)
	}
}
