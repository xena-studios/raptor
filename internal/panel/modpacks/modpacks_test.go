package modpacks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFTBAndTechnic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/modpacks/public/modpack/search/20":
			_, _ = io.WriteString(w, `{"packs":[126,999]}`)
		case "/v1/modpacks/public/modpack/popular/installs/20":
			_, _ = io.WriteString(w, `{"packs":[126]}`)
		case "/v1/modpacks/public/modpack/126":
			_, _ = io.WriteString(w, `{"id":126,"name":"Direwolf20 1.21","synopsis":"Tech and magic","installs":255061,
				"art":[{"type":"square","url":"https://cdn.feed-the-beast.com/blob/d9/x.png"},{"type":"splash","url":"https://evil.example/x.png"}],
				"versions":[{"id":100464,"name":"1.21.0","type":"release","updated":100},{"id":100596,"name":"1.23.0","type":"release","updated":300},{"id":100600,"name":"1.24.0-beta","type":"beta","updated":400}]}`)
		case "/v1/modpacks/public/modpack/999":
			_, _ = io.WriteString(w, `{"id":999,"name":"Hidden","private":true}`)
		case "/search":
			_, _ = io.WriteString(w, `{"modpacks":[{"name":"Tekkit Classic","slug":"tekkit","url":"https://www.technicpack.net/modpack/tekkit.552560","iconUrl":"https://cdn.technicpack.net/x.png"},{"name":"Bad","slug":"../x"}]}`)
		case "/modpack/tekkit":
			_, _ = io.WriteString(w, `{"displayName":"Tekkit Classic","version":"1.0","minecraft":"1.4.7"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{FTB: srv.URL, Technic: srv.URL, UserAgent: "test"}
	ctx := context.Background()
	packs, err := c.Search(ctx, "ftb", "direwolf")
	if err != nil || len(packs) != 1 || packs[0].ID != "126" || packs[0].Icon != "https://cdn.feed-the-beast.com/blob/d9/x.png" {
		t.Fatalf("FTB search (private packs left out): %+v %v", packs, err)
	}
	if popular, _ := c.Search(ctx, "ftb", ""); len(popular) != 1 {
		t.Errorf("FTB's most installed: %v", popular)
	}
	vs, err := c.Versions(ctx, "ftb", "126")
	if err != nil || len(vs) != 3 || vs[0].Name != "1.24.0-beta" || vs[0].Stable || vs[1].ID != "100596" {
		t.Errorf("FTB versions, newest first: %+v %v", vs, err)
	}
	tk, err := c.Search(ctx, "technic", "tekkit")
	if err != nil || len(tk) != 1 || tk[0].ID != "tekkit" {
		t.Errorf("Technic search (bad slugs left out): %+v %v", tk, err)
	}
	if none, _ := c.Search(ctx, "technic", ""); len(none) != 0 {
		t.Errorf("Technic without a query: %v", none)
	}
	tv, err := c.Versions(ctx, "technic", "tekkit")
	if err != nil || len(tv) != 2 || tv[0].ID != "recommended" {
		t.Errorf("Technic versions: %+v %v", tv, err)
	}
	if _, err := c.Versions(ctx, "technic", "../etc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a bad slug: %v", err)
	}
}
