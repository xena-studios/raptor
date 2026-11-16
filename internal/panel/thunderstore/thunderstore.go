// Package thunderstore is the Panel's client for Thunderstore
// (thunderstore.io, #410), where Valheim's and V Rising's BepInEx mods
// are: search, a package's versions and dependencies, and its download's
// SHA-256.
//
// Thunderstore publishes no file hashes, so the Panel downloads a version
// once, hashes it, and gives Wings that SHA-256 with the CDN's URL; Wings
// downloads the same file and keeps it only if it matches. That stops a
// file swapped between the CDN and the node, as every other source's
// hash does; like them, it trusts what the source served (docs/EGGS.md#add-ons).
package thunderstore

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

// DefaultBase is Thunderstore's site and API.
const DefaultBase = "https://thunderstore.io"

// CDNHost serves packages' files and icons (the only host Wings downloads
// Thunderstore's files from, and one the app's CSP allows for icons).
const CDNHost = "ccdn.thunderstore.io"

// Limits.
const (
	// PageSize is how many packages a search page has (Thunderstore's).
	PageSize = 20
	// MaxNames is how many packages one identification looks up.
	MaxNames = 100
	// MaxFile is the largest package the Panel hashes (Wings' limit).
	MaxFile = 256 << 20
	// hashEntries bounds the hashes kept; versions never change, so a
	// hash is kept until the cache is full.
	hashEntries = 5000
)

// Errors.
var (
	ErrUnavailable = errors.New("thunderstore isn't answering right now; try again in a minute")
	ErrNotFound    = errors.New("thunderstore doesn't have that")
	ErrTooLarge    = fmt.Errorf("thunderstore packages are at most %d MiB", MaxFile>>20)
)

// Communities are the Thunderstore communities the thunderstore add-on
// kind's loaders name: one for each game.
var Communities = []string{"valheim", "v-rising"}

// Client calls Thunderstore.
type Client struct {
	Base string
	// CDN is where package files are (empty: https://ccdn.thunderstore.io).
	CDN       string
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time

	once sync.Once
	api  *webapi.Client

	mu     sync.Mutex
	hashes map[string]File
}

func (c *Client) base() string { return cmp.Or(c.Base, DefaultBase) }
func (c *Client) cdn() string  { return cmp.Or(c.CDN, "https://"+CDNHost) }

func (c *Client) do(ctx context.Context, path string, query url.Values, out any) error {
	c.once.Do(func() {
		c.api = &webapi.Client{
			Base: c.base(), HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now,
			NotFound: ErrNotFound, Unavailable: ErrUnavailable,
		}
	})
	return c.api.Do(ctx, path, query, nil, out)
}

var (
	part    = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)
	version = regexp.MustCompile(`^[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$`)
)

// Ref names a package: Namespace-Name, and a version when it's one.
type Ref struct {
	Namespace, Name, Version string
}

// Package is "Namespace-Name", the package's ID in Raptor.
func (r Ref) Package() string { return r.Namespace + "-" + r.Name }

// String is "Namespace-Name-1.2.3" (Thunderstore's full name), or
// "Namespace-Name" without a version.
func (r Ref) String() string {
	if r.Version == "" {
		return r.Package()
	}
	return r.Package() + "-" + r.Version
}

// ParsePackage reads "Namespace-Name".
func ParsePackage(s string) (Ref, bool) {
	ns, name, ok := strings.Cut(s, "-")
	if !ok || !part.MatchString(ns) || !part.MatchString(name) {
		return Ref{}, false
	}
	return Ref{Namespace: ns, Name: name}, true
}

// ParseVersion reads "Namespace-Name-1.2.3": a version, or a dependency.
func ParseVersion(s string) (Ref, bool) {
	i := strings.LastIndexByte(s, '-')
	if i < 0 || !version.MatchString(s[i+1:]) {
		return Ref{}, false
	}
	r, ok := ParsePackage(s[:i])
	r.Version = s[i+1:]
	return r, ok
}

// Provided reports whether a dependency is the game's BepInEx pack, which
// the egg's install script installs, not Thunderstore's add-ons.
func Provided(r Ref) bool { return strings.HasPrefix(r.Name, "BepInExPack") }

// ValidCommunity reports whether c is a community the kind takes.
func ValidCommunity(c string) bool { return slices.Contains(Communities, c) }

// Category is a package's category in its community.
type Category struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Listed is a package as a community's listing shows it.
type Listed struct {
	Namespace   string     `json:"namespace"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	IconURL     string     `json:"icon_url"`
	Downloads   int64      `json:"download_count"`
	Categories  []Category `json:"categories"`
	Deprecated  bool       `json:"is_deprecated"`
	Updated     time.Time  `json:"last_updated"`
}

// Ref is the package's name.
func (l Listed) Ref() Ref { return Ref{Namespace: l.Namespace, Name: l.Name} }

// ClientOnly reports whether a package's categories say it runs only on
// players' computers: client-side and not server-side (Valheim's
// "Client-side" and "Server-side", V Rising's "Client" and "Server").
func ClientOnly(cats []Category) bool {
	client, server := false, false
	for _, c := range cats {
		switch c.Slug {
		case "client-side", "client":
			client = true
		case "server-side", "server":
			server = true
		}
	}
	return client && !server
}

// Search lists a community's packages matching query, most downloaded
// first, from offset (a multiple of PageSize).
func (c *Client) Search(ctx context.Context, community, query string, offset int) ([]Listed, int, error) {
	if !ValidCommunity(community) {
		return nil, 0, ErrNotFound
	}
	q := url.Values{"ordering": {"most-downloaded"}, "page": {fmt.Sprint(offset/PageSize + 1)}}
	if query != "" {
		q.Set("q", query)
	}
	var out struct {
		Count   int      `json:"count"`
		Results []Listed `json:"results"`
	}
	if err := c.do(ctx, "/api/cyberstorm/listing/"+community+"/", q, &out); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, 0, nil // past the last page
		}
		return nil, 0, err
	}
	return out.Results, out.Count, nil
}

// Version is one of a package's versions, with its dependencies
// (Thunderstore's full names, "Namespace-Name-1.2.3").
type Version struct {
	Namespace    string    `json:"namespace"`
	Name         string    `json:"name"`
	Number       string    `json:"version_number"`
	Description  string    `json:"description"`
	Icon         string    `json:"icon"`
	Dependencies []string  `json:"dependencies"`
	Downloads    int64     `json:"downloads"`
	Created      time.Time `json:"date_created"`
	Website      string    `json:"website_url"`
}

// Ref is the version's name.
func (v Version) Ref() Ref { return Ref{Namespace: v.Namespace, Name: v.Name, Version: v.Number} }

// Package is a package with its newest version.
type Package struct {
	Namespace  string             `json:"namespace"`
	Name       string             `json:"name"`
	Owner      string             `json:"owner"`
	Downloads  int64              `json:"total_downloads"`
	Deprecated bool               `json:"is_deprecated"`
	Latest     Version            `json:"latest"`
	Updated    time.Time          `json:"date_updated"`
	Listings   []CommunityListing `json:"community_listings"`
}

// CommunityListing is a package's listing in one community.
type CommunityListing struct {
	Community  string   `json:"community"`
	Categories []string `json:"categories"`
}

// Ref is the package's name.
func (p Package) Ref() Ref { return Ref{Namespace: p.Namespace, Name: p.Name} }

// ClientOnly reports whether the package's categories in a community
// say it runs only on players' computers.
func (p Package) ClientOnly(community string) bool {
	for _, l := range p.Listings {
		if l.Community == community {
			var cats []Category
			for _, c := range l.Categories {
				cats = append(cats, Category{Name: c, Slug: strings.ToLower(c)})
			}
			return ClientOnly(cats)
		}
	}
	return false
}

// Get is a package with its newest version.
func (c *Client) Get(ctx context.Context, r Ref) (Package, error) {
	var out Package
	err := c.do(ctx, "/api/experimental/package/"+r.Namespace+"/"+r.Name+"/", nil, &out)
	return out, err
}

// Version is one version of a package, with its dependencies.
func (c *Client) Version(ctx context.Context, r Ref) (Version, error) {
	var out Version
	if r.Version == "" {
		return out, ErrNotFound
	}
	err := c.do(ctx, "/api/experimental/package/"+r.Namespace+"/"+r.Name+"/"+r.Version+"/", nil, &out)
	return out, err
}

// Release is a version as a package's version list shows it.
type Release struct {
	Number    string    `json:"version_number"`
	Created   time.Time `json:"datetime_created"`
	Downloads int64     `json:"download_count"`
}

// Versions are a package's versions, newest first.
func (c *Client) Versions(ctx context.Context, r Ref) ([]Release, error) {
	var out []Release
	if err := c.do(ctx, "/api/cyberstorm/package/"+r.Namespace+"/"+r.Name+"/versions/", nil, &out); err != nil {
		return nil, err
	}
	out = slices.DeleteFunc(out, func(v Release) bool { return !version.MatchString(v.Number) })
	slices.SortStableFunc(out, func(a, b Release) int { return b.Created.Compare(a.Created) })
	return out, nil
}

// PageURL is a package's page in a community.
func PageURL(community string, r Ref) string {
	return DefaultBase + "/c/" + community + "/p/" + r.Namespace + "/" + r.Name + "/"
}

// DownloadURL is where Thunderstore's site downloads a version (it
// counts the download, then redirects to the CDN).
func (c *Client) DownloadURL(r Ref) string {
	return c.base() + "/package/download/" + r.Namespace + "/" + r.Name + "/" + r.Version + "/"
}

// FileURL is a version's file on the CDN, which Wings downloads.
func (c *Client) FileURL(r Ref) string {
	return c.cdn() + "/live/repository/packages/" + r.String() + ".zip"
}

// FileName is a version's file name: Namespace-Name-1.2.3.zip.
func FileName(r Ref) string { return r.String() + ".zip" }

// Icon is an icon URL on Thunderstore's CDN, or "".
func Icon(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != CDNHost {
		return ""
	}
	return raw
}

// File is a version's file as Wings is given it.
type File struct {
	URL    string
	Name   string
	Size   int64
	SHA256 string
}

// Hash downloads a version once, through Thunderstore's download link (so
// the author's download count goes up), and is its CDN URL, size, and
// SHA-256, remembered by version.
func (c *Client) Hash(ctx context.Context, r Ref) (File, error) {
	if r.Version == "" {
		return File{}, ErrNotFound
	}
	key := r.String()
	c.mu.Lock()
	f, ok := c.hashes[key]
	c.mu.Unlock()
	if ok {
		return f, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.DownloadURL(r), nil)
	if err != nil {
		return File{}, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	hc := http.Client{Timeout: 5 * time.Minute, CheckRedirect: c.redirect}
	if c.HTTP != nil {
		hc.Transport = c.HTTP.Transport
	}
	resp, err := hc.Do(req)
	if err != nil {
		return File{}, fmt.Errorf("%w (%w)", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return File{}, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return File{}, fmt.Errorf("%w (%s)", ErrUnavailable, resp.Status)
	case resp.ContentLength > MaxFile:
		return File{}, ErrTooLarge
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, MaxFile+1))
	if err != nil {
		return File{}, fmt.Errorf("%w (%w)", ErrUnavailable, err)
	}
	if n > MaxFile {
		return File{}, ErrTooLarge
	}
	// Wings gets the URL the download came from, when it's the CDN's.
	at := c.FileURL(r)
	if u := resp.Request.URL; c.redirect(resp.Request, nil) == nil && u.Path != "" {
		at = u.String()
	}
	f = File{URL: at, Name: FileName(r), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
	c.mu.Lock()
	if c.hashes == nil || len(c.hashes) >= hashEntries {
		c.hashes = map[string]File{}
	}
	c.hashes[key] = f
	c.mu.Unlock()
	return f, nil
}

// redirect follows the download link only to Thunderstore's CDN.
func (c *Client) redirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("too many redirects")
	}
	cdn, _ := url.Parse(c.cdn())
	if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, cdn.Host) {
		return fmt.Errorf("%w (it sent the download elsewhere)", ErrUnavailable)
	}
	return nil
}
