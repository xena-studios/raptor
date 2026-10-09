// Package eggimport fetches eggs from URLs and reviews them before an org
// imports them (docs/PANEL.md#imported-eggs). An egg is code that runs on
// the org's nodes, so what it runs is shown before anything is saved, and
// fetching it can't be turned against the Panel's own network.
package eggimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xena-studios/raptor/internal/eggs"
)

// MaxSize is the largest egg file taken. The biggest of the 606 surveyed
// community eggs is under 100 KiB.
const MaxSize = 1 << 20

// Errors, worded for the person importing.
var (
	ErrURL      = errors.New("enter an https:// link to an egg file")
	ErrPrivate  = errors.New("that link points to a private network address; Raptor only fetches eggs from the internet")
	ErrTooBig   = errors.New("that file is bigger than 1 MiB, which no egg is")
	ErrNotEgg   = errors.New("that isn't an egg Raptor can read; eggs are the JSON or YAML files Pterodactyl and Pelican export")
	ErrRedirect = errors.New("that link redirects too many times, or away from https")
	ErrFetch    = errors.New("couldn't download that link; check it opens in your browser")
	ErrInherits = errors.New("this egg borrows parts from another egg (Pterodactyl's \"extends\" or \"copy script from\"), which Raptor can't follow; import an egg that has everything in it")
)

// blocked are ranges that aren't private by netip's reckoning but still
// aren't the internet, or can lead back into a private network.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64: can embed a private IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("2002::/16"),      // 6to4: can embed a private IPv4
}

// Public reports whether an address is on the internet: not loopback,
// private, link-local (the cloud metadata endpoint), or otherwise local.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// Client is an HTTP client that only reaches public addresses. The check
// is on the address actually dialed, after DNS, so a name that resolves
// to a private address (at first or on a second lookup) is refused.
func Client() *http.Client {
	d := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			a, err := netip.ParseAddr(host)
			if err != nil || !Public(a) {
				return ErrPrivate
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:                  nil, // a proxy would dial for us, unchecked
			DialContext:            d.DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  10 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
			ForceAttemptHTTP2:      true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" {
				return ErrRedirect
			}
			return nil
		},
	}
}

// githubFile is a file's page on GitHub: github.com/<owner>/<repo>/blob/<ref>/<path>.
var githubFile = regexp.MustCompile(`^/([^/]+)/([^/]+)/(?:blob|raw)/(.+)$`)

// ParseURL checks a link and turns a GitHub file page into its raw file.
// Only https on the usual port: an egg import isn't a port scanner.
func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return nil, ErrURL
	}
	if a, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); err == nil && !Public(a) {
		return nil, ErrPrivate
	}
	if strings.EqualFold(u.Hostname(), "github.com") {
		if m := githubFile.FindStringSubmatch(u.Path); m != nil {
			u = &url.URL{Scheme: "https", Host: "raw.githubusercontent.com", Path: "/" + m[1] + "/" + m[2] + "/" + m[3]}
		}
	}
	u.Fragment = ""
	return u, nil
}

// Fetch downloads an egg file. It returns the file and the URL it came
// from after redirects.
func Fetch(ctx context.Context, c *http.Client, u *url.URL) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", ErrURL
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/plain;q=0.9, */*;q=0.1")
	req.Header.Set("User-Agent", "Raptor-Panel (egg import)")
	res, err := c.Do(req)
	if err != nil {
		for _, e := range []error{ErrPrivate, ErrRedirect} {
			if errors.Is(err, e) {
				return nil, "", e
			}
		}
		return nil, "", ErrFetch
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("that link answered %q instead of a file", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxSize+1))
	if err != nil {
		return nil, "", ErrFetch
	}
	if len(data) > MaxSize {
		return nil, "", ErrTooBig
	}
	return data, res.Request.URL.String(), nil
}

// Sum is a file's SHA-256, in hex.
func Sum(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Warning is something to look at before importing.
type Warning struct {
	Kind string
	Text string
}

// Review is what an egg would run.
type Review struct {
	Egg        *eggs.Egg
	Registries []string
	Warnings   []Warning
}

// knownImages are the publishers the community eggs' images come from.
var knownImages = []string{
	"ghcr.io/pterodactyl/", "ghcr.io/parkervcp/", "ghcr.io/pelican-eggs/", "ghcr.io/ptero-eggs/",
	"quay.io/pterodactyl/",
}

// pipeToShell is a download piped straight into a shell.
var pipeToShell = regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`)

// Parse reads and reviews an egg file.
func Parse(data []byte) (*Review, error) {
	if len(data) > MaxSize {
		return nil, ErrTooBig
	}
	egg, err := eggs.Parse(data)
	if err != nil {
		return nil, ErrNotEgg
	}
	if inherits(data) {
		return nil, ErrInherits
	}
	r := &Review{Egg: egg}
	refs := make([]string, 0, len(egg.Images)+1)
	for _, i := range egg.Images {
		refs = append(refs, i.Ref)
	}
	if egg.Install.Container != "" {
		refs = append(refs, egg.Install.Container)
	}
	var unknown []string
	for _, ref := range refs {
		reg := Registry(ref)
		if !slices.Contains(r.Registries, reg) {
			r.Registries = append(r.Registries, reg)
		}
		if !knownImage(ref) && !slices.Contains(unknown, ref) {
			unknown = append(unknown, ref)
		}
	}
	for _, ref := range unknown {
		r.Warnings = append(r.Warnings, Warning{"registry", fmt.Sprintf(
			"%s isn't from a publisher the community eggs use. It runs on your node with your server's files; make sure you trust whoever publishes it.", ref)})
	}
	if pipeToShell.MatchString(egg.Install.Script) {
		r.Warnings = append(r.Warnings, Warning{
			"pipe_to_shell",
			"The install script downloads a script and runs it. What that script does can change after you import the egg.",
		})
	}
	if len(egg.Raptor.Arch) == 0 {
		r.Warnings = append(r.Warnings, Warning{
			"no_arch",
			"The egg doesn't say which CPUs it runs on. If its images are x86-only, servers on ARM nodes won't start.",
		})
	}
	if egg.Raptor.Certified {
		r.Warnings = append(r.Warnings, Warning{
			"claims_certified",
			"The egg calls itself certified. Only eggs in Raptor's own catalog are certified; this one will show as imported.",
		})
	}
	return r, nil
}

// inherits reports whether an egg leaves parts to another egg, by ID: on
// a node, those parts would just be missing.
func inherits(data []byte) bool {
	var e struct {
		Config struct {
			Extends any `yaml:"extends"`
		} `yaml:"config"`
		Scripts struct {
			Installation struct {
				CopyFrom any `yaml:"copy_script_from"`
			} `yaml:"installation"`
		} `yaml:"scripts"`
		CopyFrom any `yaml:"copy_script_from"`
	}
	if yaml.Unmarshal(data, &e) != nil {
		return false
	}
	set := func(v any) bool { return v != nil && v != "" && v != 0 }
	return set(e.Config.Extends) || set(e.Scripts.Installation.CopyFrom) || set(e.CopyFrom)
}

// Registry is the registry an image comes from: "docker.io" for Docker
// Hub, whose images can leave it out.
func Registry(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")
	first, _, found := strings.Cut(ref, "/")
	if !found || (!strings.ContainsAny(first, ".:") && first != "localhost") {
		return "docker.io"
	}
	first = strings.ToLower(first)
	if first == "index.docker.io" || first == "registry-1.docker.io" {
		return "docker.io"
	}
	return first
}

// knownImage reports whether an image is from the community eggs'
// publishers, or one of Docker Hub's official images (debian, alpine,
// node, python, ...).
func knownImage(ref string) bool {
	l := strings.ToLower(ref)
	for _, p := range knownImages {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	l = strings.TrimPrefix(strings.TrimPrefix(l, "docker.io/"), "library/")
	return Registry(ref) == "docker.io" && !strings.Contains(l, "/")
}
