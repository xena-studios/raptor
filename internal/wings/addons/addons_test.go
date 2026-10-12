package addons_test

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/addons"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const srvID = "0199a0b1-7c2e-7a41-9f3e-2b1c4d5e6f70"

// servers is a fake server manager with one Paper server.
type servers struct{ dir string }

func (s *servers) FilesDir(string) (string, error)                  { return s.dir, nil }
func (s *servers) CheckFiles(context.Context, string, bool) error   { return nil }
func (s *servers) Denylist(string) ([]string, error)                { return []string{"plugins/locked.jar"}, nil }
func (s *servers) SpaceLeft(context.Context, string) (int64, error) { return -1, nil }
func (s *servers) TrashDir(id string) (string, error) {
	return filepath.Join(filepath.Dir(s.dir), ".trash", id), nil
}

func (s *servers) Egg(context.Context, string) (*eggs.Egg, error) {
	return &eggs.Egg{Raptor: eggs.Extension{Addons: map[string]eggs.Addon{
		"plugins": {Loaders: []string{"paper"}, Dir: "plugins", Version: "MINECRAFT_VERSION"},
	}}}, nil
}

func sum(b []byte) string {
	h := sha512.Sum512(b)
	return hex.EncodeToString(h[:])
}

func sum256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestInstall(t *testing.T) {
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
		ID: srvID, Name: "smp", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x",
		Variables: "{}", Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed",
	}); err != nil {
		t.Fatal(err)
	}
	srv := &servers{dir: dir}
	fsvc := files.NewService(files.Options{Servers: srv, Store: db, Events: events.New(db), UID: os.Getuid(), GID: os.Getgid()})

	// "Modrinth's CDN": cdn.example.com, answered by a local server.
	v1, v2 := []byte("plugin version 1"), []byte("plugin version 2")
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.jar":
			_, _ = w.Write(v1)
		case "/v2.jar":
			_, _ = w.Write(v2)
		case "/elsewhere":
			http.Redirect(w, r, "https://evil.example.net/x.jar", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdn.Close()
	tr := cdn.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "example.com" // the test certificate's name, for every host
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, cdn.Listener.Addr().String())
	}
	svc := &addons.Service{Files: fsvc, Servers: srv, Hosts: []string{"example.com", addons.HangarHost}}
	svc.Client = &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error {
		return addons.ErrSource // the default client only follows redirects to its hosts
	}}

	install := func(p addons.Install) (addons.File, error) {
		p.Kind = "plugins"
		return svc.Install(ctx, srvID, "user", p)
	}
	if _, err := install(addons.Install{URL: "https://example.com/v1.jar", Sha512: sum(v1), Filename: "Hello-1.jar"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "plugins", "Hello-1.jar")); err != nil || string(got) != string(v1) {
		t.Fatalf("installed: %q, %v", got, err)
	}

	for name, p := range map[string]addons.Install{
		"another host":   {URL: "https://evil.example.net/v1.jar", Sha512: sum(v1), Filename: "a.jar"},
		"plain http":     {URL: "http://example.com/v1.jar", Sha512: sum(v1), Filename: "a.jar"},
		"another port":   {URL: "https://example.com:8443/v1.jar", Sha512: sum(v1), Filename: "a.jar"},
		"a redirect out": {URL: "https://example.com/elsewhere", Sha512: sum(v1), Filename: "a.jar"},
	} {
		if _, err := install(p); err == nil {
			t.Errorf("%s: installed", name)
		}
	}
	if _, err := install(addons.Install{URL: "https://example.com/v2.jar", Sha512: sum(v1), Filename: "swapped.jar"}); !errors.Is(err, addons.ErrHash) {
		t.Errorf("a file that isn't the one asked for: %v", err)
	}
	for _, name := range []string{"../escape.jar", "sub/dir.jar", ".hidden.jar", "notajar.sh", ""} {
		if _, err := install(addons.Install{URL: "https://example.com/v1.jar", Sha512: sum(v1), Filename: name}); !errors.Is(err, addons.ErrName) {
			t.Errorf("file name %q: %v", name, err)
		}
	}
	if _, err := install(addons.Install{URL: "https://example.com/v1.jar", Sha512: sum(v1), Filename: "locked.jar"}); err == nil {
		t.Error("wrote a file on the egg's denylist")
	}
	if _, err := svc.Install(ctx, srvID, "user", addons.Install{Kind: "mods", URL: "https://example.com/v1.jar", Sha512: sum(v1), Filename: "a.jar"}); !errors.Is(err, addons.ErrNoAddon) {
		t.Errorf("an add-on the egg doesn't have: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "plugins", ".raptor-*")); len(left) > 0 {
		t.Errorf("downloads left behind: %v", left)
	}

	// Hangar lists SHA-256s; Modrinth's files still need their SHA-512.
	if _, err := install(addons.Install{URL: "https://" + addons.HangarHost + "/v1.jar", Sha512: sum(v1), Filename: "h.jar"}); err == nil {
		t.Error("a Hangar file without its SHA-256")
	}
	if _, err := install(addons.Install{URL: "https://example.com/v1.jar", Sha256: sum256(v1), Filename: "h.jar"}); err == nil {
		t.Error("a Modrinth file with only a SHA-256")
	}
	if _, err := install(addons.Install{URL: "https://" + addons.HangarHost + "/v2.jar", Sha256: sum256(v1), Filename: "h.jar"}); !errors.Is(err, addons.ErrHash) {
		t.Errorf("a swapped Hangar file: %v", err)
	}
	if _, err := install(addons.Install{URL: "https://" + addons.HangarHost + "/v1.jar", Sha256: sum256(v1), Filename: "FromHangar.jar"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "plugins", "FromHangar.jar")); err != nil {
		t.Fatal(err)
	}

	// Updating: the new version in, the old one to the trash.
	if _, err := install(addons.Install{URL: "https://example.com/v2.jar", Sha512: sum(v2), Filename: "Hello-2.jar", Replace: "Hello-1.jar"}); err != nil {
		t.Fatal(err)
	}
	list, err := svc.List(ctx, srvID, "plugins")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Files) != 1 || list.Files[0].Name != "Hello-2.jar" || list.Files[0].Sha512 != sum(v2) || list.Files[0].Sha256 != sum256(v2) || list.Dir != "plugins" {
		t.Errorf("list: %+v", list)
	}
	trash, err := fsvc.ListTrash(ctx, srvID)
	if err != nil || len(trash) != 1 {
		t.Errorf("trash: %+v, %v", trash, err)
	}
}
