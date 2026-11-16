package gameversions

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/projects/paper":
			_, _ = io.WriteString(w, `{"project":{},"versions":{"26.3":["26.3","26.3-rc-3"],"26.2":["26.2.1","26.2"]}}`)
		case "/v2/purpur":
			_, _ = io.WriteString(w, `{"versions":["26.1.2","26.2","26.3"]}`)
		case "/mc/game/version_manifest_v2.json":
			_, _ = io.WriteString(w, `{"versions":[{"id":"26.4-snapshot-8","type":"snapshot"},{"id":"26.3","type":"release"},{"id":"b1.7","type":"old_beta"}]}`)
		case "/v2/versions/game":
			_, _ = io.WriteString(w, `[{"version":"26.4-snapshot-8","stable":false},{"version":"26.3","stable":true}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{Bases: &Bases{PaperMC: srv.URL, Purpur: srv.URL, Mojang: srv.URL, Fabric: srv.URL, Quilt: srv.URL}, UserAgent: "test"}
	ctx := context.Background()
	ids := func(source string) []Version {
		t.Helper()
		v, err := c.List(ctx, source)
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
		return v
	}
	if got := ids("paper"); len(got) != 4 || got[0] != (Version{"26.3", true}) || got[1].Stable || got[2].ID != "26.2.1" {
		t.Errorf("paper, newest family first: %v", got)
	}
	if got := ids("purpur"); got[0].ID != "26.3" || got[2].ID != "26.1.2" {
		t.Errorf("purpur, newest first: %v", got)
	}
	if got := ids("vanilla"); len(got) != 2 || got[0].Stable || !got[1].Stable {
		t.Errorf("vanilla, releases and snapshots only: %v", got)
	}
	if got := ids("fabric"); got[1] != (Version{"26.3", true}) {
		t.Errorf("fabric: %v", got)
	}
	if _, err := c.List(ctx, "nope"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown: %v", err)
	}
}
