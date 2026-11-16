package thunderstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRefs(t *testing.T) {
	r, ok := ParseVersion("ValheimModding-Jotunn-2.30.2")
	if !ok || r.Namespace != "ValheimModding" || r.Name != "Jotunn" || r.Version != "2.30.2" || r.Package() != "ValheimModding-Jotunn" {
		t.Errorf("ParseVersion: %+v %v", r, ok)
	}
	if FileName(r) != "ValheimModding-Jotunn-2.30.2.zip" {
		t.Errorf("FileName: %s", FileName(r))
	}
	for _, bad := range []string{"", "Jotunn", "a-b", "a-b-1.0", "a-b-1.0.0-beta", "../x-y-1.0.0", "a/b-c-1.0.0", "a-b-c-1.0.0"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("ParseVersion(%q) is a version", bad)
		}
	}
	if p, ok := ParsePackage("denikson-BepInExPack_Valheim"); !ok || !Provided(p) {
		t.Errorf("the BepInEx pack: %+v %v", p, ok)
	}
	if _, ok := ParsePackage("a-b-c"); ok {
		t.Error("a-b-c is a package")
	}
	if !ClientOnly([]Category{{Slug: "client-side"}}) || ClientOnly([]Category{{Slug: "client-side"}, {Slug: "server-side"}}) || ClientOnly(nil) || !ClientOnly([]Category{{Slug: "client"}}) {
		t.Error("ClientOnly")
	}
	if Icon("https://ccdn.thunderstore.io/live/repository/icons/x.png") == "" || Icon("https://evil.example.net/x.png") != "" || Icon("http://ccdn.thunderstore.io/x.png") != "" {
		t.Error("Icon")
	}
}

func TestClient(t *testing.T) {
	zip := []byte("PK\x03\x04 a package")
	var downloads int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cyberstorm/listing/valheim/":
			if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("q") != "jot" {
				http.Error(w, "bad query "+r.URL.RawQuery, http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"count":21,"results":[{"namespace":"ValheimModding","name":"Jotunn","download_count":5,
				"icon_url":"https://ccdn.thunderstore.io/i.png","categories":[{"name":"Libraries","slug":"libraries"}]}]}`)
		case "/api/cyberstorm/package/ValheimModding/Jotunn/versions/":
			_, _ = io.WriteString(w, `[{"version_number":"2.1.0","datetime_created":"2021-06-29T00:00:00Z"},
				{"version_number":"2.30.2","datetime_created":"2026-09-21T00:00:00Z"},{"version_number":"bad","datetime_created":"2026-10-01T00:00:00Z"}]`)
		case "/package/download/ValheimModding/Jotunn/2.30.2/":
			downloads++
			http.Redirect(w, r, "/live/repository/packages/ValheimModding-Jotunn-2.30.2.zip", http.StatusFound)
		case "/live/repository/packages/ValheimModding-Jotunn-2.30.2.zip":
			_, _ = w.Write(zip)
		case "/package/download/Evil/Mod/1.0.0/":
			http.Redirect(w, r, "https://evil.example.net/x.zip", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, CDN: srv.URL, HTTP: srv.Client(), UserAgent: "test"}
	ctx := context.Background()

	list, total, err := c.Search(ctx, "valheim", "jot", PageSize)
	if err != nil || total != 21 || len(list) != 1 || list[0].Ref().Package() != "ValheimModding-Jotunn" {
		t.Fatalf("search: %+v %d %v", list, total, err)
	}
	if _, _, err := c.Search(ctx, "lethal-company", "", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("another community: %v", err)
	}
	vs, err := c.Versions(ctx, Ref{Namespace: "ValheimModding", Name: "Jotunn"})
	if err != nil || len(vs) != 2 || vs[0].Number != "2.30.2" {
		t.Fatalf("versions (newest first, bad ones left out): %+v %v", vs, err)
	}

	r := Ref{Namespace: "ValheimModding", Name: "Jotunn", Version: "2.30.2"}
	f, err := c.Hash(ctx, r)
	sum := sha256.Sum256(zip)
	if err != nil || f.SHA256 != hex.EncodeToString(sum[:]) || f.Size != int64(len(zip)) || f.Name != "ValheimModding-Jotunn-2.30.2.zip" ||
		f.URL != srv.URL+"/live/repository/packages/ValheimModding-Jotunn-2.30.2.zip" {
		t.Fatalf("hash: %+v %v", f, err)
	}
	if _, err := c.Hash(ctx, r); err != nil || downloads != 1 {
		t.Errorf("a version is downloaded once: %d, %v", downloads, err)
	}
	if _, err := c.Hash(ctx, Ref{Namespace: "Evil", Name: "Mod", Version: "1.0.0"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a download sent elsewhere: %v", err)
	}
	if _, err := c.Hash(ctx, Ref{Namespace: "No", Name: "Such", Version: "1.0.0"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing version: %v", err)
	}
}
