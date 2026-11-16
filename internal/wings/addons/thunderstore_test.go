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
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/addons"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// valheim is a fake server manager with one Valheim server.
type valheim struct{ servers }

func (v *valheim) Egg(context.Context, string) (*eggs.Egg, error) {
	return &eggs.Egg{Raptor: eggs.Extension{Addons: map[string]eggs.Addon{
		"thunderstore": {Loaders: []string{"valheim"}, Dir: "BepInEx/plugins"},
	}}}, nil
}

type entry struct {
	name string
	body string
	mode os.FileMode
}

func zipOf(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func thunderstoreSetup(t *testing.T, zips map[string][]byte) (*addons.Service, *files.Service, string) {
	t.Helper()
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
		ID: srvID, Name: "valheim", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x",
		Variables: "{}", Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed",
	}); err != nil {
		t.Fatal(err)
	}
	srv := &valheim{servers{dir: dir}}
	fsvc := files.NewService(files.Options{Servers: srv, Store: db, Events: events.New(db), UID: os.Getuid(), GID: os.Getgid()})
	cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := zips[strings.TrimPrefix(r.URL.Path, "/live/repository/packages/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(cdn.Close)
	tr := cdn.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, cdn.Listener.Addr().String())
	}
	svc := &addons.Service{Files: fsvc, Servers: srv, Hosts: []string{addons.ThunderstoreHost, "example.com"}}
	svc.Client = &http.Client{Transport: tr}
	return svc, fsvc, dir
}

func cdnURL(file string) string {
	return "https://" + addons.ThunderstoreHost + "/live/repository/packages/" + file
}

func TestThunderstoreInstall(t *testing.T) {
	ctx := context.Background()
	v1 := zipOf(t,
		entry{name: "manifest.json", body: `{"name":"Jotunn","version_number":"2.20.0"}`},
		entry{name: "plugins/"},
		entry{name: `plugins\Jotunn.dll`, body: "dll v1"},
	)
	v2 := zipOf(t, entry{name: "manifest.json", body: "{}"}, entry{name: "Jotunn.dll", body: "dll v2"})
	svc, fsvc, dir := thunderstoreSetup(t, map[string][]byte{
		"ValheimModding-Jotunn-2.20.0.zip": v1,
		"ValheimModding-Jotunn-2.30.2.zip": v2,
	})
	install := func(p addons.Install) (addons.File, error) {
		p.Kind = "thunderstore"
		return svc.Install(ctx, srvID, "user", p)
	}
	f, err := install(addons.Install{URL: cdnURL("ValheimModding-Jotunn-2.20.0.zip"), Sha256: sum256(v1), Filename: "ValheimModding-Jotunn-2.20.0.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "ValheimModding-Jotunn" || f.Package != "ValheimModding-Jotunn-2.20.0" || f.Sha512 != sum(v1) {
		t.Errorf("installed: %+v", f)
	}
	// Backslashes are folders, as on Windows.
	pkg := filepath.Join(dir, "BepInEx", "plugins", "ValheimModding-Jotunn")
	if got, err := os.ReadFile(filepath.Join(pkg, "plugins", "Jotunn.dll")); err != nil || string(got) != "dll v1" {
		t.Fatalf("unpacked: %q, %v", got, err)
	}
	// A folder put there by hand is listed without a name or hashes.
	if err := os.MkdirAll(filepath.Join(dir, "BepInEx", "plugins", "ByHand"), 0o755); err != nil {
		t.Fatal(err)
	}
	ls, err := svc.List(ctx, srvID, "thunderstore")
	if err != nil || ls.Dir != "BepInEx/plugins" || len(ls.Files) != 2 {
		t.Fatalf("list: %+v, %v", ls, err)
	}
	for _, x := range ls.Files {
		switch x.Name {
		case "ValheimModding-Jotunn":
			if x.Package != "ValheimModding-Jotunn-2.20.0" || x.Sha512 != sum(v1) || x.Sha256 != sum256(v1) || x.Size != int64(len(v1)) {
				t.Errorf("listed: %+v", x)
			}
		case "ByHand":
			if x.Package != "" || x.Sha512 != "" {
				t.Errorf("a folder added by hand: %+v", x)
			}
		}
	}

	// The hash is the one the Panel took; the host is Thunderstore's.
	if _, err := install(addons.Install{URL: cdnURL("ValheimModding-Jotunn-2.30.2.zip"), Sha256: sum256(v1), Filename: "ValheimModding-Jotunn-2.30.2.zip"}); !errors.Is(err, addons.ErrHash) {
		t.Errorf("a swapped package: %v", err)
	}
	if _, err := install(addons.Install{URL: "https://example.com/live/repository/packages/ValheimModding-Jotunn-2.30.2.zip", Sha256: sum256(v2), Filename: "ValheimModding-Jotunn-2.30.2.zip"}); !errors.Is(err, addons.ErrSource) {
		t.Errorf("a package from elsewhere: %v", err)
	}
	if _, err := install(addons.Install{URL: cdnURL("ValheimModding-Jotunn-2.30.2.zip"), Sha512: sum(v2), Filename: "ValheimModding-Jotunn-2.30.2.zip"}); err == nil {
		t.Error("a package without its SHA-256")
	}
	for _, name := range []string{"Jotunn.zip", "../x-y-1.0.0.zip", "a-b-1.0.zip", "a-b-1.0.0.jar"} {
		if _, err := install(addons.Install{URL: cdnURL("ValheimModding-Jotunn-2.30.2.zip"), Sha256: sum256(v2), Filename: name}); !errors.Is(err, addons.ErrName) {
			t.Errorf("file name %q: %v", name, err)
		}
	}
	if _, err := install(addons.Install{URL: cdnURL("ValheimModding-Jotunn-2.30.2.zip"), Sha256: sum256(v2), Filename: "ValheimModding-Jotunn-2.30.2.zip", Replace: "Other-Mod"}); !errors.Is(err, addons.ErrName) {
		t.Errorf("replacing another package: %v", err)
	}
	// Thunderstore's CDN isn't a source for other kinds.
	if _, err := svc.Install(ctx, srvID, "user", addons.Install{Kind: "plugins", URL: cdnURL("x.jar"), Sha256: sum256(v1), Filename: "x.jar"}); err == nil {
		t.Error("a jar from Thunderstore for another kind")
	}

	// Updating: the new version in its place, the old folder to the trash.
	if _, err := install(addons.Install{URL: cdnURL("ValheimModding-Jotunn-2.30.2.zip"), Sha256: sum256(v2), Filename: "ValheimModding-Jotunn-2.30.2.zip", Replace: "ValheimModding-Jotunn"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(pkg, "Jotunn.dll")); err != nil || string(got) != "dll v2" {
		t.Errorf("updated: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(pkg, "plugins")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old version's files are still there: %v", err)
	}
	if trash, err := fsvc.ListTrash(ctx, srvID); err != nil || len(trash) != 1 {
		t.Errorf("trash: %+v, %v", trash, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "BepInEx", "plugins", ".raptor-*")); len(left) > 0 {
		t.Errorf("downloads left behind: %v", left)
	}
}

func TestThunderstoreUnsafe(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		zip  []byte
		want error
	}{
		"climbing out":       {zipOf(t, entry{name: "../../evil.dll", body: "x"}), addons.ErrUnsafe},
		"climbing out (\\)":  {zipOf(t, entry{name: `plugins\..\..\evil.dll`, body: "x"}), addons.ErrUnsafe},
		"an absolute path":   {zipOf(t, entry{name: "/etc/evil", body: "x"}), addons.ErrUnsafe},
		"a drive":            {zipOf(t, entry{name: `C:\evil.dll`, body: "x"}), addons.ErrUnsafe},
		"Wings' record":      {zipOf(t, entry{name: addons.RecordName, body: "{}"}), addons.ErrUnsafe},
		"empty segment (//)": {zipOf(t, entry{name: "a//b.dll", body: "x"}), addons.ErrUnsafe},
		"one safe, one not":  {zipOf(t, entry{name: "ok.dll", body: "x"}, entry{name: "a/../../b", body: "x"}), addons.ErrUnsafe},
		"too many files":     {zipOf(t, entry{name: "a"}, entry{name: "b"}, entry{name: "c"}, entry{name: "d"}, entry{name: "e"}), addons.ErrTooManyFiles},
		"one file too big":   {zipOf(t, entry{name: "big.bin", body: strings.Repeat("x", 200)}), addons.ErrUnpackTooLong},
		"too big together":   {zipOf(t, entry{name: "a.bin", body: strings.Repeat("x", 60)}, entry{name: "b.bin", body: strings.Repeat("x", 60)}), addons.ErrUnpackTooLong},
		"not a zip":          {[]byte("not a zip at all"), addons.ErrNotZip},
		"a link isn't made":  {zipOf(t, entry{name: "link", body: "/etc/passwd", mode: os.ModeSymlink | 0o777}, entry{name: "ok.dll", body: "x"}), nil},
		"within the limits":  {zipOf(t, entry{name: "a/b/c.dll", body: strings.Repeat("x", 90)}), nil},
	}
	zips := map[string][]byte{}
	files := map[string]string{}
	i := 0
	for name, c := range cases {
		file := "Bad-Pkg" + string(rune('A'+i)) + "-1.0.0.zip"
		i++
		zips[file] = c.zip
		files[name] = file
	}
	svc, _, dir := thunderstoreSetup(t, zips)
	svc.Unpack = addons.Limits{Entries: 4, Bytes: 100}
	plugins := filepath.Join(dir, "BepInEx", "plugins")
	for name, c := range cases {
		file := files[name]
		_, err := svc.Install(ctx, srvID, "user", addons.Install{Kind: "thunderstore", URL: cdnURL(file), Sha256: sum256(c.zip), Filename: file})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		// Nothing is kept of a package that's refused.
		if _, serr := os.Stat(filepath.Join(plugins, strings.TrimSuffix(file, "-1.0.0.zip"))); (c.want == nil) != (serr == nil) {
			t.Errorf("%s: its folder: %v", name, serr)
		}
	}
	for _, p := range []string{filepath.Join(dir, "evil.dll"), filepath.Join(dir, "BepInEx", "evil.dll"), filepath.Join(dir, "b")} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("a file landed outside the package: %s", p)
		}
	}
	link := filepath.Join(plugins, strings.TrimSuffix(files["a link isn't made"], "-1.0.0.zip"), "link")
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a link was made: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(plugins, ".raptor-*")); len(left) > 0 {
		t.Errorf("left behind: %v", left)
	}
}
