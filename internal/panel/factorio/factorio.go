// Package factorio is the Panel's client for Factorio's mod portal
// (mods.factorio.com, #411): search, a mod's releases with their SHA-1s and
// dependencies, and what an installed file is. The portal's API has no
// search, so the Panel keeps the whole list (about 10,000 mods), refreshed
// hourly, and searches it itself. Downloads need a factorio.com account;
// Wings adds it from the server's variables.
package factorio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/panel/webapi"
)

// DefaultBase is the mod portal.
const DefaultBase = "https://mods.factorio.com"

// AssetsBase is where mods' thumbnails are (the app's CSP allows it).
const AssetsBase = "https://assets-mod.factorio.com"

// Limits.
const (
	PageSize = 20
	// MaxNames is how many files one identification looks up.
	MaxNames = 100
	listTTL  = time.Hour
	maxList  = 64 << 20
)

// Errors.
var (
	ErrUnavailable = errors.New("the Factorio mod portal isn't answering right now; try again in a minute")
	ErrNotFound    = errors.New("the Factorio mod portal doesn't have that")
)

// Official are Factorio's own: the base game and Space Age's parts, which
// come with the game, not the portal.
var Official = []string{"base", "space-age", "quality", "elevated-rails"}

// Client calls the mod portal.
type Client struct {
	Base      string
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time

	once sync.Once
	api  *webapi.Client

	mu     sync.Mutex
	list   []Mod
	listAt time.Time
}

// Mod is one of the portal's mods, as its list says.
type Mod struct {
	Name      string  `json:"name"`
	Title     string  `json:"title"`
	Owner     string  `json:"owner"`
	Summary   string  `json:"summary"`
	Downloads int64   `json:"downloads_count"`
	Latest    Release `json:"latest_release"`
	Thumbnail string  `json:"thumbnail"`
}

// Release is a version of a mod.
type Release struct {
	DownloadURL string    `json:"download_url"`
	FileName    string    `json:"file_name"`
	Version     string    `json:"version"`
	SHA1        string    `json:"sha1"`
	Released    time.Time `json:"released_at"`
	Info        struct {
		FactorioVersion string   `json:"factorio_version"`
		Dependencies    []string `json:"dependencies"`
	} `json:"info_json"`
}

func (c *Client) base() string { return cmp.Or(c.Base, DefaultBase) }

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) do(ctx context.Context, path string, out any) error {
	c.once.Do(func() {
		c.api = &webapi.Client{
			Base: c.base(), HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now,
			NotFound: ErrNotFound, Unavailable: ErrUnavailable,
		}
	})
	return c.api.Do(ctx, path, nil, nil, out)
}

// all is every mod, from the cache or the portal.
func (c *Client) all(ctx context.Context) ([]Mod, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list != nil && c.now().Sub(c.listAt) < listTTL {
		return c.list, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/api/mods?page_size=max", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		if c.list != nil {
			return c.list, nil // an old list beats none
		}
		return nil, ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Results []Mod `json:"results"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, maxList)).Decode(&out) != nil {
		if c.list != nil {
			return c.list, nil
		}
		return nil, ErrUnavailable
	}
	slices.SortFunc(out.Results, func(a, b Mod) int { return cmp.Compare(b.Downloads, a.Downloads) })
	c.list, c.listAt = out.Results, c.now()
	return c.list, nil
}

// Major is a Factorio version's major.minor ("2.0" for 2.0.72); "" for
// anything else (latest, experimental), which matches any.
func Major(version string) string {
	m := regexp.MustCompile(`^(\d+\.\d+)(\.\d+)?$`).FindStringSubmatch(version)
	if m == nil {
		return ""
	}
	return m[1]
}

// Search lists mods matching query (name, title, or summary) whose newest
// release is for the Factorio major (any, if empty), most downloaded first.
func (c *Client) Search(ctx context.Context, query, major string, offset int) ([]Mod, int, error) {
	list, err := c.all(ctx)
	if err != nil {
		return nil, 0, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var hits []Mod
	for _, m := range list {
		if m.Latest.FileName == "" || (major != "" && m.Latest.Info.FactorioVersion != major) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(m.Name), q) && !strings.Contains(strings.ToLower(m.Title), q) && !strings.Contains(strings.ToLower(m.Summary), q) {
			continue
		}
		hits = append(hits, m)
	}
	offset = min(max(offset, 0), len(hits))
	return hits[offset:min(offset+PageSize, len(hits))], len(hits), nil
}

// Full is a mod with all its releases, oldest first.
type Full struct {
	Mod
	Releases []Release `json:"releases"`
}

var name = regexp.MustCompile(`^[A-Za-z0-9 _\-]{1,100}$`)

// ValidName reports whether s can be a mod's name.
func ValidName(s string) bool { return name.MatchString(s) }

// Get is a mod with its releases.
func (c *Client) Get(ctx context.Context, mod string) (Full, error) {
	var out Full
	if !ValidName(mod) {
		return out, ErrNotFound
	}
	err := c.do(ctx, "/api/mods/"+url.PathEscape(mod)+"/full", &out)
	return out, err
}

// Releases are a mod's releases for a major (all, if empty), newest first,
// that Wings can install: a .zip from the portal, with a SHA-1.
func (f Full) ReleasesFor(major string) []Release {
	var out []Release
	for _, r := range slices.Backward(f.Releases) {
		if (major == "" || r.Info.FactorioVersion == major) && Installable(r) {
			out = append(out, r)
		}
	}
	return out
}

var sha1Hex = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Installable reports whether Wings can install a release.
func Installable(r Release) bool {
	return strings.HasPrefix(r.DownloadURL, "/download/") && sha1Hex.MatchString(r.SHA1) &&
		strings.HasSuffix(r.FileName, ".zip") && !strings.ContainsAny(r.FileName, `/\`)
}

// Dependency is one line of a release's dependencies.
type Dependency struct {
	Name string
	// "required", "optional", or "incompatible".
	Type string
}

var dep = regexp.MustCompile(`^\s*(\(\?\)|\?|!|~)?\s*(.+?)\s*(?:(?:<=|>=|=|<|>)\s*[0-9.]+)?\s*$`)

// Dependencies are a release's, without the game's own (base, Space Age).
func Dependencies(r Release) []Dependency {
	var out []Dependency
	for _, d := range r.Info.Dependencies {
		m := dep.FindStringSubmatch(d)
		if m == nil || slices.Contains(Official, m[2]) {
			continue
		}
		typ := "required"
		switch m[1] {
		case "?", "(?)":
			typ = "optional"
		case "!":
			typ = "incompatible"
		}
		out = append(out, Dependency{Name: m[2], Type: typ})
	}
	return out
}

var fileName = regexp.MustCompile(`^(.+)_(\d+\.\d+\.\d+)\.zip$`)

// ParseFile is the mod and version a file is named for (Krastorio2_2.1.4.zip).
func ParseFile(file string) (mod, version string, ok bool) {
	m := fileName.FindStringSubmatch(file)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// Icon is a mod's thumbnail, or "" for none.
func (m Mod) Icon() string {
	if m.Thumbnail == "" || !strings.HasPrefix(m.Thumbnail, "/assets/") || strings.Contains(m.Thumbnail, "blank") {
		return ""
	}
	return AssetsBase + m.Thumbnail
}
