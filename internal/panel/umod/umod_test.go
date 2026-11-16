package umod

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins/search.json":
			if r.URL.Query().Get("categories[]") != "rust" || r.URL.Query().Get("sort") != "downloads" {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"total":2,"data":[
				{"name":"KitsPlus","title":"Kits Plus","slug":"kits-plus"},
				{"name":"Kits","title":"Rust Kits","slug":"rust-kits","download_url":"https://umod.org/plugins/Kits.cs","latest_release_version":"4.4.10",
				 "latest_release_version_checksum":"735e62b66430192cb96a9465fa7acdb9c57a9ca1","latest_release_at_atom":"2026-10-01T12:00:00+00:00"}]}`)
		case "/plugins/rust-kits.json":
			_, _ = io.WriteString(w, `{"name":"Kits","slug":"rust-kits","download_url":"https://evil.example/Kits.cs","latest_release_version_checksum":"735e62b66430192cb96a9465fa7acdb9c57a9ca1"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, UserAgent: "test"}
	ctx := context.Background()
	p, ok, err := c.ByName(ctx, "rust", "Kits")
	if err != nil || !ok || p.Slug != "rust-kits" || !p.Installable() || p.Released().IsZero() {
		t.Fatalf("ByName: %+v %v %v", p, ok, err)
	}
	if _, ok, _ := c.ByName(ctx, "rust", "../x"); ok {
		t.Error("a name that isn't one")
	}
	if _, ok, _ := c.ByName(ctx, "rust", "Nope"); ok {
		t.Error("a name no hit has")
	}
	got, err := c.Get(ctx, "rust-kits")
	if err != nil || got.Installable() {
		t.Errorf("a download from elsewhere is installable: %+v %v", got, err)
	}
	if _, err := c.Get(ctx, "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}
