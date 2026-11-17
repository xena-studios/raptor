package addons_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/addons"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/store"
)

func nupkg(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// OpenMod's plugins: the package is checked by its SHA-512, and only its
// one plugin assembly is written, under the name the Panel gives it.
func TestInstallNuGet(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "server")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Write.InsertServer(ctx, store.InsertServerParams{
		ID: srvID, Name: "unturned", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x",
		Variables: "{}", Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed",
	}); err != nil {
		t.Fatal(err)
	}
	srv := &servers{dir: dir}
	fsvc := files.NewService(files.Options{Servers: srv, Store: db, Events: events.New(db), UID: os.Getuid(), GID: os.Getgid()})

	good := nupkg(t, map[string]string{
		"Kits.nuspec": "<package/>", "lib/net461/Kits.dll": "net461", "lib/netstandard2.1/Kits.dll": "the plugin",
		"lib/netstandard2.1/Kits.xml": "docs", "content/evil.dll": "no", ".signature.p7s": "sig",
	})
	pkgs := map[string][]byte{
		"/good.nupkg":   good,
		"/two.nupkg":    nupkg(t, map[string]string{"lib/net461/A.dll": "a", "lib/net461/B.dll": "b"}),
		"/escape.nupkg": nupkg(t, map[string]string{"lib/net461/../../../../escape.dll": "x", "../lib/net461/A.dll": "x"}),
		"/none.nupkg":   nupkg(t, map[string]string{"lib/net8.0/A.dll": "a"}),
		"/notzip.nupkg": []byte("not a zip"),
	}
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := pkgs[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	defer cdn.Close()
	tr := cdn.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, cdn.Listener.Addr().String())
	}
	svc := &addons.Service{Files: fsvc, Servers: srv, Hosts: []string{addons.NuGetHost, addons.ModrinthHost}, Client: &http.Client{Transport: tr}}
	plugins := filepath.Join(dir, "Servers", "Raptor", "OpenMod", "plugins")
	install := func(p string, body []byte, name string) (addons.File, error) {
		return svc.Install(ctx, srvID, "user", addons.Install{Kind: "openmod", URL: "https://" + addons.NuGetHost + p, Sha512: sum(body), Filename: name})
	}

	f, err := install("/good.nupkg", good, "DiFFoZ.Kits@1.0.0.dll")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(plugins, f.Name)); err != nil || string(got) != "the plugin" {
		t.Fatalf("installed: %q, %v", got, err)
	}
	ls, err := svc.List(ctx, srvID, "openmod")
	if err != nil || len(ls.Files) != 1 || ls.Files[0].Sha512 != sum([]byte("the plugin")) {
		t.Fatalf("listing: %+v, %v", ls, err)
	}

	if _, err := svc.Install(ctx, srvID, "user", addons.Install{Kind: "openmod", URL: "https://" + addons.NuGetHost + "/good.nupkg", Sha512: sum(good[1:]), Filename: "A@1.0.0.dll"}); !errors.Is(err, addons.ErrHash) {
		t.Errorf("a swapped package: %v", err)
	}
	for p, want := range map[string]error{"/two.nupkg": addons.ErrPackage, "/escape.nupkg": addons.ErrPackage, "/none.nupkg": addons.ErrPackage} {
		if _, err := install(p, pkgs[p], "A@1.0.0.dll"); !errors.Is(err, want) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := install("/notzip.nupkg", pkgs["/notzip.nupkg"], "A@1.0.0.dll"); err == nil {
		t.Error("installed from something that isn't a zip")
	}
	if _, err := install("/good.nupkg", good, "../A@1.0.0.dll"); !errors.Is(err, addons.ErrName) {
		t.Errorf("a name outside the folder: %v", err)
	}
	// NuGet is for OpenMod only, and OpenMod's plugins come from NuGet only.
	if _, err := svc.Install(ctx, srvID, "user", addons.Install{Kind: "plugins", URL: "https://" + addons.NuGetHost + "/good.nupkg", Sha512: sum(good), Filename: "a.jar"}); !errors.Is(err, addons.ErrSource) {
		t.Errorf("a NuGet package as a Paper plugin: %v", err)
	}
	if _, err := svc.Install(ctx, srvID, "user", addons.Install{Kind: "openmod", URL: "https://" + addons.ModrinthHost + "/good.nupkg", Sha512: sum(good), Filename: "A@1.0.0.dll"}); !errors.Is(err, addons.ErrSource) {
		t.Errorf("an OpenMod plugin from Modrinth: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape.dll")); err == nil {
		t.Error("a package wrote outside the server")
	}
	if left, _ := filepath.Glob(filepath.Join(plugins, ".raptor-*")); len(left) > 0 {
		t.Errorf("downloads left behind: %v", left)
	}
	if names, _ := filepath.Glob(filepath.Join(plugins, "*")); len(names) != 1 {
		t.Errorf("files in the folder: %v", names)
	}
}
