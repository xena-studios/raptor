package files

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/jobs"
)

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"1.1.1.1":               true,
		"2606:4700::1111":       true,
		"127.0.0.1":             false,
		"10.1.2.3":              false,
		"172.16.0.1":            false,
		"192.168.1.1":           false,
		"169.254.169.254":       false, // cloud metadata
		"100.100.100.100":       false, // carrier-grade NAT
		"0.0.0.0":               false,
		"255.255.255.255":       false,
		"::1":                   false,
		"fd00::1":               false,
		"fe80::1":               false,
		"::ffff:127.0.0.1":      false, // an IPv4 address mapped into IPv6
		"::ffff:8.8.8.8":        true,
		"64:ff9b::a9fe:a9fe":    false, // NAT64 for 169.254.169.254
		"2002:7f00:1::":         false, // 6to4 for 127.0.0.1
		"224.0.0.1":             false,
		"ff02::1":               false,
		"198.18.0.1":            false,
		"2001:db8::1":           false,
		"fec0::1":               false,
		"203.0.113.7":           false,
		"8.8.4.4":               true,
		"2a00:1450:4001:80b::1": true,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v", addr, got)
		}
	}
}

func TestPullURLs(t *testing.T) {
	for _, bad := range []string{
		"", "ftp://example.com/a", "file:///etc/passwd", "example.com/a", "http://", "https://user:pw@example.com/a",
		"http://127.0.0.1/a", "http://[::1]/a", "http://169.254.169.254/latest/meta-data/", "http://10.0.0.5:8080/x",
		"https://example.com/" + strings.Repeat("a", maxURL),
	} {
		if _, err := parsePullURL(bad, Public); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
	u, err := parsePullURL(" https://cdn.example.com/files/Paper%201.21.jar?x=1 ", Public)
	if err != nil {
		t.Fatal(err)
	}
	if got := pullName(u); got != "Paper 1.21.jar" {
		t.Errorf("name %q", got)
	}
	u, _ = parsePullURL("https://example.com/", Public)
	if got := pullName(u); got != "download" {
		t.Errorf("name %q", got)
	}
}

func TestPull(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugin.jar":
			_, _ = w.Write([]byte("jar bytes"))
		case "/moved":
			http.Redirect(w, r, "/plugin.jar", http.StatusFound)
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 100)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	// The test server is on the loopback, which downloads never reach:
	// say so first, then allow it.
	localhost := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	id, err := e.svc.Pull(ctx, srvID, "u1", localhost+"/plugin.jar", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(id); j.Status != jobs.Failed || !strings.Contains(j.Error, "public internet") {
		t.Fatalf("a download from the loopback: %s %q", j.Status, j.Error)
	}
	if _, err := e.svc.Pull(ctx, srvID, "u1", srv.URL+"/plugin.jar", "", ""); !errors.Is(err, ErrNotPublic) {
		t.Fatalf("a loopback address typed in: %v", err)
	}
	e.svc.o.PullAllowed = func(ip netip.Addr) bool { return ip.IsLoopback() }

	e.write("plugins/.keep", "")
	id, err = e.svc.Pull(ctx, srvID, "u1", srv.URL+"/moved", "plugins", "LuckPerms.jar")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(id); j.Status != jobs.Succeeded {
		t.Fatalf("download: %s %q", j.Status, j.Error)
	}
	if got := e.read("plugins/LuckPerms.jar"); got != "jar bytes" {
		t.Errorf("downloaded %q", got)
	}
	st, err := e.svc.PullStatus(ctx, srvID, id)
	if err != nil || st.Status != jobs.Succeeded || st.Path != "plugins/LuckPerms.jar" {
		t.Errorf("status: %+v, %v", st, err)
	}
	if _, err := e.svc.PullStatus(ctx, "another", id); !errors.Is(err, ErrTransferNotFound) {
		t.Errorf("another server's download: %v", err)
	}

	// The name from the URL; a taken one is refused.
	id, err = e.svc.Pull(ctx, srvID, "u1", srv.URL+"/plugin.jar", "", "")
	if err != nil {
		t.Fatal(err)
	}
	e.wait(id)
	if !e.exists("plugin.jar") {
		t.Error("no plugin.jar")
	}
	if _, err := e.svc.Pull(ctx, srvID, "u1", srv.URL+"/plugin.jar", "", ""); !errors.Is(err, fs.ErrExist) {
		t.Errorf("a taken name: %v", err)
	}
	for _, name := range []string{"../x", "a/b", ".raptor-upload-x", ".."} {
		if _, err := e.svc.Pull(ctx, srvID, "u1", srv.URL+"/plugin.jar", "", name); err == nil {
			t.Errorf("name %q was taken", name)
		}
	}
	if _, err := e.svc.Pull(ctx, srvID, "u1", srv.URL+"/plugin.jar", "plugin.jar", "x"); err == nil {
		t.Error("a file as the folder")
	}

	// A 404, and a file bigger than the space left: nothing is left behind.
	id, _ = e.svc.Pull(ctx, srvID, "u1", srv.URL+"/gone", "", "gone.jar")
	if j := e.wait(id); j.Status != jobs.Failed || !strings.Contains(j.Error, "404") {
		t.Errorf("a 404: %s %q", j.Status, j.Error)
	}
	e.servers.mu.Lock()
	e.servers.space = 10
	e.servers.mu.Unlock()
	id, _ = e.svc.Pull(ctx, srvID, "u1", srv.URL+"/big", "", "big.bin")
	if j := e.wait(id); j.Status != jobs.Failed || !strings.Contains(j.Error, "disk space") {
		t.Errorf("no space: %s %q", j.Status, j.Error)
	}
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range entries {
		if IsStaging(d.Name()) || d.Name() == "big.bin" || d.Name() == "gone.jar" {
			t.Errorf("left behind: %s", d.Name())
		}
	}
}
