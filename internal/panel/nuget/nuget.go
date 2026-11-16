// Package nuget is the Panel's client for nuget.org, where OpenMod's
// plugins are published (#415, docs/EGGS.md#add-ons): search for packages
// tagged openmod-plugin, a package's versions with their dependencies, and
// each version's SHA-512 and files from NuGet's catalog. Answers are
// cached (webapi); catalog entries never change.
package nuget

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/panel/webapi"
	"github.com/xena-studios/raptor/internal/shared/nupkg"
)

// Where NuGet answers: its search service, and everything else (package
// metadata, the catalog, and the downloads Wings fetches).
const (
	DefaultSearch = "https://azuresearch-usnc.nuget.org"
	DefaultAPI    = "https://api.nuget.org"
)

// Tag marks an OpenMod plugin on NuGet (OpenMod's own `openmod install`
// searches it too).
const Tag = "openmod-plugin"

// Limits.
const (
	PageSize = 20
	// MaxNames is how many files one identification looks up.
	MaxNames = 50
	// maxPages is how many pages of a package's versions are read.
	maxPages = 10
)

// Errors.
var (
	ErrUnavailable = errors.New("nuget isn't answering right now; try again in a minute")
	ErrNotFound    = errors.New("nuget doesn't have that")
)

// Client calls nuget.org.
type Client struct {
	// Search and API are NuGet's addresses (empty: the defaults).
	Search, API string
	HTTP        *http.Client
	UserAgent   string
	Now         func() time.Time

	once           sync.Once
	search, origin *webapi.Client
}

func (c *Client) apiBase() string { return strings.TrimSuffix(cmp.Or(c.API, DefaultAPI), "/") }

func (c *Client) init() {
	c.once.Do(func() {
		mk := func(base string) *webapi.Client {
			return &webapi.Client{
				Base: strings.TrimSuffix(base, "/"), HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now,
				NotFound: ErrNotFound, Unavailable: ErrUnavailable,
			}
		}
		c.search, c.origin = mk(cmp.Or(c.Search, DefaultSearch)), mk(c.apiBase())
	})
}

// get fetches one of NuGet's own absolute URLs (the links its answers
// carry), only from its API's host.
func (c *Client) get(ctx context.Context, link string, out any) error {
	c.init()
	rest, ok := strings.CutPrefix(link, c.apiBase()+"/")
	if !ok || strings.Contains(rest, "..") {
		return ErrUnavailable
	}
	return c.origin.Do(ctx, "/"+rest, nil, nil, out)
}

// Package is a search result: a package at its newest version.
type Package struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	IconURL     string   `json:"iconUrl"`
	Downloads   int64    `json:"totalDownloads"`
	Authors     Authors  `json:"authors"`
	Tags        []string `json:"tags"`
}

// Authors is a package's authors: NuGet answers a string or a list.
type Authors []string

// UnmarshalJSON implements json.Unmarshaler.
func (a *Authors) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = Authors{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

// Icon is the package's icon when NuGet hosts it, or "" (the app's CSP
// allows images from NuGet's API only).
func Icon(p Package) string {
	if strings.HasPrefix(p.IconURL, DefaultAPI+"/v3-flatcontainer/") {
		return p.IconURL
	}
	return ""
}

// SearchPlugins lists OpenMod plugins matching query, NuGet's order (most
// relevant, then most downloaded).
func (c *Client) SearchPlugins(ctx context.Context, query string, offset int) ([]Package, int, error) {
	c.init()
	q := url.Values{
		"q": {strings.TrimSpace("tags:" + Tag + " " + query)}, "skip": {strconv.Itoa(max(offset, 0))},
		"take": {strconv.Itoa(PageSize)}, "prerelease": {"false"}, "semVerLevel": {"2.0.0"},
	}
	var out struct {
		Total int       `json:"totalHits"`
		Data  []Package `json:"data"`
	}
	if err := c.search.Do(ctx, "/query", q, nil, &out); err != nil {
		return nil, 0, err
	}
	// The tag filter is NuGet's; keep only what it says is tagged.
	hits := slices.DeleteFunc(out.Data, func(p Package) bool {
		return !nupkg.ValidID(p.ID) || !slices.ContainsFunc(p.Tags, func(t string) bool { return strings.EqualFold(t, Tag) })
	})
	return hits, out.Total, nil
}

// Dependency is one package a version needs.
type Dependency struct {
	ID    string `json:"id"`
	Range string `json:"range"`
}

// Version is one of a package's versions, as its registration lists it.
type Version struct {
	// Catalog is the version's catalog entry, with its hash and files.
	Catalog        string    `json:"@id"`
	ID             string    `json:"id"`
	Version        string    `json:"version"`
	Title          string    `json:"title"`
	Description    string    `json:"description"`
	Authors        string    `json:"authors"`
	IconURL        string    `json:"iconUrl"`
	Listed         *bool     `json:"listed"`
	Published      time.Time `json:"published"`
	PackageContent string    `json:"packageContent"`
	Tags           []string  `json:"tags"`
	Groups         []struct {
		Dependencies []Dependency `json:"dependencies"`
	} `json:"dependencyGroups"`
}

// Prerelease reports whether it's a pre-release (1.0.0-beta).
func (v Version) Prerelease() bool { return strings.Contains(v.Version, "-") }

// Plugin reports whether the package is tagged an OpenMod plugin.
func (v Version) Plugin() bool {
	return slices.ContainsFunc(v.Tags, func(t string) bool { return strings.EqualFold(t, Tag) })
}

// Dependencies are the packages it needs that OpenMod doesn't bring:
// OpenMod's own packages, .NET's, and the libraries OpenMod ships are
// left out.
func (v Version) Dependencies() []Dependency {
	var out []Dependency
	for _, g := range v.Groups {
		for _, d := range g.Dependencies {
			if !Provided(d.ID) && nupkg.ValidID(d.ID) && !slices.ContainsFunc(out, func(o Dependency) bool { return strings.EqualFold(o.ID, d.ID) }) {
				out = append(out, d)
			}
		}
	}
	return out
}

// provided are the packages (by prefix, lower case) OpenMod or the game
// bring, which a plugin's dependencies name but nothing installs.
var provided = []string{"openmod.", "microsoft.", "system.", "netstandard.library", "newtonsoft.json", "unityengine", "nuget.", "autofac", "yamldotnet", "serilog", "lib.harmony"}

// Provided reports whether OpenMod or the game bring a package.
func Provided(id string) bool {
	id = strings.ToLower(id)
	return slices.ContainsFunc(provided, func(p string) bool { return strings.HasPrefix(id, p) })
}

type registration struct {
	Items []struct {
		ID    string `json:"@id"`
		Items []struct {
			Entry Version `json:"catalogEntry"`
		} `json:"items"`
	} `json:"items"`
}

// Versions are a package's listed versions, newest first.
func (c *Client) Versions(ctx context.Context, pkg string) ([]Version, error) {
	if !nupkg.ValidID(pkg) {
		return nil, ErrNotFound
	}
	c.init()
	var reg registration
	if err := c.origin.Do(ctx, "/v3/registration5-gz-semver2/"+strings.ToLower(pkg)+"/index.json", nil, nil, &reg); err != nil {
		return nil, err
	}
	var out []Version
	for i, page := range reg.Items {
		if i == maxPages {
			break
		}
		items := page.Items
		if items == nil { // a big package's pages are linked, not inlined
			var p struct {
				Items []struct {
					Entry Version `json:"catalogEntry"`
				} `json:"items"`
			}
			if err := c.get(ctx, page.ID, &p); err != nil {
				return nil, err
			}
			items = p.Items
		}
		for _, it := range items {
			if v := it.Entry; (v.Listed == nil || *v.Listed) && strings.EqualFold(v.ID, pkg) && nupkg.ValidVersion(v.Version) {
				out = append(out, v)
			}
		}
	}
	slices.Reverse(out) // NuGet lists them oldest first
	return out, nil
}

// sha512Base64 is a SHA-512 as NuGet's catalog writes it.
var sha512Base64 = regexp.MustCompile(`^[A-Za-z0-9+/]{86}==$`)

// File is a version's package: where Wings downloads it, its SHA-512
// (hex), and the plugin inside it.
type File struct {
	URL    string
	Sha512 string
	// Entry is the plugin's assembly in the package, and Size its size.
	Entry string
	Size  int64
}

// File is a version's package, from its catalog entry; ok is false when
// it isn't one Wings can install (no SHA-512, or no single plugin
// assembly for Unturned's frameworks).
func (c *Client) File(ctx context.Context, v Version) (File, bool, error) {
	var entry struct {
		Hash      string `json:"packageHash"`
		Algorithm string `json:"packageHashAlgorithm"`
		Entries   []struct {
			Name   string `json:"fullName"`
			Length int64  `json:"length"`
		} `json:"packageEntries"`
	}
	content := c.apiBase() + "/v3-flatcontainer/"
	if !strings.HasPrefix(v.PackageContent, content) || !strings.HasSuffix(v.PackageContent, ".nupkg") || strings.Contains(v.PackageContent, "..") {
		return File{}, false, nil
	}
	if err := c.get(ctx, v.Catalog, &entry); err != nil {
		if errors.Is(err, ErrNotFound) {
			return File{}, false, nil
		}
		return File{}, false, err
	}
	// A version without NuGet's SHA-512 isn't offered.
	if !strings.EqualFold(entry.Algorithm, "SHA512") || !sha512Base64.MatchString(entry.Hash) {
		return File{}, false, nil
	}
	sum, _ := base64.StdEncoding.DecodeString(entry.Hash) // checked above
	names := make([]string, 0, len(entry.Entries))
	for _, e := range entry.Entries {
		names = append(names, e.Name)
	}
	dll, ok := nupkg.PluginDLL(names)
	if !ok {
		return File{}, false, nil
	}
	out := File{URL: v.PackageContent, Sha512: hex.EncodeToString(sum), Entry: dll}
	for _, e := range entry.Entries {
		if e.Name == dll {
			out.Size = e.Length
		}
	}
	return out, true, nil
}
