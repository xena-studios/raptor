// Package umod is the Panel's client for uMod (umod.org), where Rust's
// Oxide and Carbon plugins come from (#409, docs/EGGS.md#add-ons): search,
// a plugin's details, and finding a plugin by its file's name. uMod
// publishes only each plugin's newest file, with its SHA-1. Answers are
// cached (webapi).
package umod

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/panel/webapi"
)

// DefaultBase is uMod's site, which answers JSON.
const DefaultBase = "https://umod.org"

// Limits.
const (
	// PageSize is how many plugins a search returns (uMod's own page).
	PageSize = 10
	// MaxNames is how many files one identification looks up: one search
	// each.
	MaxNames = 50
)

// Host is where plugins download from, and IconHost where their icons
// are (the app's CSP allows images from it).
const (
	Host     = "umod.org"
	IconHost = "assets.umod.org"
)

// Errors.
var (
	ErrUnavailable = errors.New("umod isn't answering right now; try again in a minute")
	ErrNotFound    = errors.New("umod doesn't have that")
)

// Client calls uMod.
type Client struct {
	// Base is uMod's address (empty: DefaultBase).
	Base string
	// HTTP makes the requests (nil: a client with a 15 second timeout).
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time

	once sync.Once
	api  *webapi.Client
}

func (c *Client) do(ctx context.Context, path string, query url.Values, out any) error {
	c.once.Do(func() {
		base := c.Base
		if base == "" {
			base = DefaultBase
		}
		c.api = &webapi.Client{
			Base: base, HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now,
			NotFound: ErrNotFound, Unavailable: ErrUnavailable,
		}
	})
	return c.api.Do(ctx, path, query, nil, out)
}

// Plugin is a uMod plugin, with its newest file.
type Plugin struct {
	// Name is its class and file name (Kits for Kits.cs).
	Name        string `json:"name"`
	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	Author      string `json:"author"`
	Downloads   int64  `json:"downloads"`
	URL         string `json:"url"`
	DownloadURL string `json:"download_url"`
	Version     string `json:"latest_release_version"`
	SHA1        string `json:"latest_release_version_checksum"`
	ReleasedAt  string `json:"latest_release_at_atom"`
}

var (
	name = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	sha1 = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Installable reports whether Wings can install the plugin: a .cs file
// from umod.org named for the plugin, with a SHA-1.
func (p Plugin) Installable() bool {
	u, err := url.Parse(p.DownloadURL)
	return err == nil && u.Scheme == "https" && u.Host == Host && name.MatchString(p.Name) &&
		sha1.MatchString(p.SHA1) && strings.HasSuffix(u.Path, "/"+p.Name+".cs")
}

// Released is when its newest file came out (zero if uMod doesn't say).
func (p Plugin) Released() time.Time {
	t, _ := time.Parse(time.RFC3339, p.ReleasedAt)
	return t
}

// Search lists a game's plugins, most downloaded first, matching query if
// given; page counts from 1.
func (c *Client) Search(ctx context.Context, game, query string, page int) ([]Plugin, int, error) {
	q := url.Values{
		"query": {query}, "page": {strconv.Itoa(max(page, 1))},
		"sort": {"downloads"}, "sortdir": {"desc"}, "categories[]": {game},
	}
	var out struct {
		Data  []Plugin `json:"data"`
		Total int      `json:"total"`
	}
	if err := c.do(ctx, "/plugins/search.json", q, &out); err != nil {
		return nil, 0, err
	}
	return out.Data, out.Total, nil
}

// Get is a plugin by its slug.
func (c *Client) Get(ctx context.Context, slug string) (Plugin, error) {
	var p Plugin
	err := c.do(ctx, "/plugins/"+url.PathEscape(slug)+".json", nil, &p)
	return p, err
}

// ByName finds a game's plugin by its name (a file Kits.cs is Kits): the
// search hit with exactly that name.
func (c *Client) ByName(ctx context.Context, game, pluginName string) (Plugin, bool, error) {
	if !name.MatchString(pluginName) {
		return Plugin{}, false, nil
	}
	hits, _, err := c.Search(ctx, game, pluginName, 1)
	if err != nil {
		return Plugin{}, false, err
	}
	for _, p := range hits {
		if p.Name == pluginName {
			return p, true, nil
		}
	}
	return Plugin{}, false, nil
}
