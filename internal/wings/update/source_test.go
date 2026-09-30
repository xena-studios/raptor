package update

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLatest(t *testing.T) {
	releases := []Release{
		{Tag: "v1.2.0"},
		{Tag: "v1.10.0-rc.1", Prerelease: true},
		{Tag: "v1.9.1"},
		{Tag: "v1.9.2-beta.1"}, // a pre-release by its tag, whatever GitHub says
		{Tag: "nightly"},
		{Tag: "v2.0.0+meta"},
	}
	for channel, want := range map[string]string{"stable": "v1.9.1", "beta": "v1.10.0-rc.1"} {
		got, err := Latest(releases, channel)
		if err != nil || got.Tag != want {
			t.Errorf("%s: got %v, %v; want %s", channel, got, err, want)
		}
	}
	if _, err := Latest([]Release{{Tag: "v1.0.0-rc.1", Prerelease: true}}, "stable"); err == nil {
		t.Error("stable picked a pre-release")
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"1.4.2": "v1.4.2", "v1.4.2": "v1.4.2", " 1.5.0-rc.1 ": "v1.5.0-rc.1"} {
		if got, err := normalize(in); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "dev", "1.4", "1.4.2+x", "../1.0.0", "1.0.0/../../x", "vv1.0.0"} {
		if _, err := normalize(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			_, _ = w.Write([]byte(`[{"tag_name":"v1.1.0","draft":true},{"tag_name":"v1.0.0"},{"tag_name":"v1.1.0-rc.1","prerelease":true}]`))
		case "/o/r/releases/download/v1.0.0/checksums.txt":
			_, _ = w.Write([]byte("sums"))
		case "/o/r/releases/download/v1.0.0/big":
			_, _ = w.Write(bytes.Repeat([]byte("x"), 11))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g := &GitHub{Repo: "o/r", APIURL: srv.URL, DownloadURL: srv.URL, Client: srv.Client()}
	ctx := context.Background()

	rs, err := g.Releases(ctx)
	if err != nil || len(rs) != 2 || rs[0].Tag != "v1.0.0" || !rs[1].Prerelease {
		t.Fatalf("releases: %v %v", rs, err)
	}
	var b strings.Builder
	if err := g.Download(ctx, "v1.0.0", "checksums.txt", 10, &b); err != nil || b.String() != "sums" {
		t.Fatalf("download: %q %v", b.String(), err)
	}
	if err := g.Download(ctx, "v1.0.0", "big", 10, &b); err == nil {
		t.Fatal("over the limit")
	}
	if err := g.Download(ctx, "v1.0.0", "missing", 10, &b); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing: %v", err)
	}
}
