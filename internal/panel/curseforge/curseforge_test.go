package curseforge

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	var gotKey, gotQuery string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		switch r.URL.Path {
		case "/v1/mods/search":
			gotQuery = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"data":[{"id":238222,"classId":6,"name":"JEI","slug":"jei","downloadCount":5,
				"logo":{"thumbnailUrl":"https://media.forgecdn.net/x.png"},"authors":[{"name":"mezz"}]}],
				"pagination":{"totalCount":99999}}`))
		case "/v1/mods/238222/files":
			_, _ = w.Write([]byte(`{"data":[
				{"id":1,"modId":238222,"fileName":"old.jar","fileDate":"2024-01-01T00:00:00Z","isAvailable":true,
				 "downloadUrl":"https://edge.forgecdn.net/files/1/old.jar","hashes":[{"value":"AA` + sha40[2:] + `","algo":1}],
				 "gameVersions":["1.20.1","Fabric","Server"],"dependencies":[{"modId":7,"relationType":3}]},
				{"id":2,"modId":238222,"fileName":"new.jar","fileDate":"2025-01-01T00:00:00Z","isAvailable":true,
				 "downloadUrl":"https://evil.example/new.jar","hashes":[{"value":"` + sha40 + `","algo":1}]},
				{"id":3,"modId":238222,"fileName":"gone.jar","isAvailable":false}]}`))
		case "/v1/fingerprints/432":
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			_, _ = w.Write([]byte(`{"data":{"exactMatches":[{"file":{"id":1,"modId":238222,"fileFingerprint":42}}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &Client{Key: "k", Base: srv.URL}
	if !c.On() || (&Client{}).On() {
		t.Fatal("On")
	}
	mods, total, err := c.Search(t.Context(), ClassMods, "fabric", "1.20.1", "jei", 0)
	if err != nil || len(mods) != 1 || total != maxIndex || gotKey != "k" {
		t.Fatalf("search: %v %d %v key=%q", mods, total, err, gotKey)
	}
	if gotQuery != "classId=6&gameId=432&gameVersion=1.20.1&index=0&modLoaderType=4&pageSize=20&searchFilter=jei&sortField=2&sortOrder=desc" {
		t.Errorf("query %s", gotQuery)
	}
	m := mods[0]
	if m.Icon() == "" || m.Author() != "mezz" || !m.Distributable() || m.Page() != "https://www.curseforge.com/minecraft/mc-mods/jei" {
		t.Errorf("mod %+v", m)
	}
	files, err := c.Files(t.Context(), 238222, "fabric", "")
	if err != nil || len(files) != 2 || files[0].ID != 2 {
		t.Fatalf("files: %+v %v", files, err)
	}
	if files[0].Installable() {
		t.Error("a file off CurseForge's CDN is installable")
	}
	old := files[1]
	if !old.Installable() || old.SHA1() != "aa"+sha40[2:] {
		t.Errorf("old: %v %q", old.Installable(), old.SHA1())
	}
	if v, l := old.Versions(), old.Loaders(); len(v) != 1 || v[0] != "1.20.1" || len(l) != 1 || l[0] != "fabric" {
		t.Errorf("versions %v loaders %v", v, l)
	}
	if _, _, err := c.Search(t.Context(), ClassMods, "", "", "", maxIndex); err != nil {
		t.Error(err)
	}
	byFP, err := c.ByFingerprint(t.Context(), []uint32{42})
	if err != nil || byFP[42].ID != 1 || gotBody["fingerprints"] == nil {
		t.Errorf("fingerprints: %v %v %v", byFP, err, gotBody)
	}
	if _, err := c.Mod(t.Context(), 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing mod: %v", err)
	}
}

const sha40 = "0123456789abcdef0123456789abcdef01234567"
