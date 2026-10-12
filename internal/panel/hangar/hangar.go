// Package hangar is the Panel's client for Hangar, PaperMC's plugin
// repository (https://hangar.papermc.io/api-docs), for the plugin
// installer (docs/EGGS.md#add-ons): search, a project's versions, and what
// a file is by its SHA-256. Answers are cached (webapi).
package hangar

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/panel/webapi"
)

// DefaultBase is Hangar's API.
const DefaultBase = "https://hangar.papermc.io/api/v1"

// Limits.
const (
	// PageSize is how many projects a search returns.
	PageSize = 20
	// MaxHashes is how many files one lookup takes: Hangar looks files up
	// one at a time.
	MaxHashes     = 50
	versionsLimit = 25
)

// Errors.
var (
	ErrUnavailable = errors.New("hangar isn't answering right now; try again in a minute")
	ErrNotFound    = errors.New("hangar doesn't have that")
)

// Client calls Hangar.
type Client struct {
	// Base is the API's address (empty: DefaultBase).
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

// Platform is Hangar's platform for a Modrinth loader name: Paper and the
// servers that load its plugins, Velocity, or Waterfall (BungeeCord's
// fork). "" for loaders Hangar doesn't serve.
func Platform(loader string) string {
	switch loader {
	case "paper", "spigot", "bukkit", "purpur", "folia":
		return "PAPER"
	case "velocity":
		return "VELOCITY"
	case "bungeecord", "waterfall":
		return "WATERFALL"
	}
	return ""
}

// Platforms are the Hangar platforms for loaders, without repeats.
func Platforms(loaders []string) []string {
	var out []string
	for _, l := range loaders {
		if p := Platform(l); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// Project is a Hangar project.
type Project struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Namespace struct {
		Owner string `json:"owner"`
		Slug  string `json:"slug"`
	} `json:"namespace"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatarUrl"`
	Stats       struct {
		Downloads int64 `json:"downloads"`
	} `json:"stats"`
}

// URL is the project's page.
func (p Project) URL() string {
	return "https://hangar.papermc.io/" + url.PathEscape(p.Namespace.Owner) + "/" + url.PathEscape(p.Namespace.Slug)
}

// Version is one of a project's versions.
type Version struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Channel   struct {
		Name string `json:"name"`
	} `json:"channel"`
	// Downloads are the version's file for each platform.
	Downloads map[string]Download `json:"downloads"`
	// PlatformDependencies are the game versions each platform runs it on.
	PlatformDependencies map[string][]string `json:"platformDependencies"`
}

// Download is a version's file for a platform. Files hosted elsewhere
// (ExternalURL) have no hash, so they're never installed.
type Download struct {
	FileInfo *struct {
		Name      string `json:"name"`
		SizeBytes int64  `json:"sizeBytes"`
		Sha256    string `json:"sha256Hash"`
	} `json:"fileInfo"`
	ExternalURL *string `json:"externalUrl"`
	DownloadURL *string `json:"downloadUrl"`
}

// File is a version's downloadable file for the first of platforms that
// has one hosted on Hangar.
func (v Version) File(platforms []string) (Download, string, bool) {
	for _, p := range platforms {
		d, ok := v.Downloads[p]
		if ok && d.FileInfo != nil && d.DownloadURL != nil && d.ExternalURL == nil &&
			strings.HasPrefix(*d.DownloadURL, "https://hangarcdn.papermc.io/") {
			return d, p, true
		}
	}
	return Download{}, "", false
}

// Search searches projects for a platform, and a game version if given,
// most downloaded first unless there's a query.
func (c *Client) Search(ctx context.Context, platform, gameVersion, query string, offset int) ([]Project, int, error) {
	q := url.Values{"limit": {strconv.Itoa(PageSize)}, "offset": {strconv.Itoa(offset)}, "platform": {platform}}
	if gameVersion != "" {
		q.Set("version", gameVersion)
	}
	if query != "" {
		q.Set("query", query)
		q.Set("prioritizeExactMatch", "true")
	} else {
		q.Set("sort", "-downloads")
	}
	var out struct {
		Pagination struct {
			Count int `json:"count"`
		} `json:"pagination"`
		Result []Project `json:"result"`
	}
	if err := c.do(ctx, "/projects", q, &out); err != nil {
		return nil, 0, err
	}
	return out.Result, out.Pagination.Count, nil
}

// Versions lists a project's versions for a platform and a game version
// (if given), newest first, leaving out hidden channels.
func (c *Client) Versions(ctx context.Context, project, platform, gameVersion string) ([]Version, error) {
	q := url.Values{"limit": {strconv.Itoa(versionsLimit)}, "platform": {platform}, "includeHiddenChannels": {"false"}}
	if gameVersion != "" {
		q.Set("platformVersion", gameVersion)
	}
	var out struct {
		Result []Version `json:"result"`
	}
	err := c.do(ctx, "/projects/"+url.PathEscape(project)+"/versions", q, &out)
	return out.Result, err
}

// ByHash returns the project a file belongs to, by SHA-256.
func (c *Client) ByHash(ctx context.Context, sha256 string) (Project, error) {
	var out Project
	err := c.do(ctx, "/versions/hash/"+url.PathEscape(sha256), nil, &out)
	return out, err
}
