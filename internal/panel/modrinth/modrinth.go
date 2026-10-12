// Package modrinth is the Panel's client for Modrinth's API
// (https://docs.modrinth.com/api/), for the plugin installer and mod
// manager (docs/EGGS.md#add-ons): search, a project's versions, and what
// a file is by its SHA-512. Answers are cached (webapi).
package modrinth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/panel/webapi"
)

// DefaultBase is Modrinth's API.
const DefaultBase = "https://api.modrinth.com/v2"

// Limits.
const (
	// PageSize is how many projects a search returns.
	PageSize = 20
	// MaxHashes is how many files one lookup takes.
	MaxHashes = 500
)

// ErrUnavailable is Modrinth not answering as it should.
var ErrUnavailable = errors.New("modrinth isn't answering right now; try again in a minute")

// Client calls Modrinth.
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

func (c *Client) do(ctx context.Context, path string, query url.Values, body any, out any) error {
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
	return c.api.Do(ctx, path, query, body, out)
}

// ErrNotFound is a project or version Modrinth doesn't have.
var ErrNotFound = errors.New("modrinth doesn't have that")

// Project is a Modrinth project, as search and /projects describe it.
type Project struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"` // search hits name the ID this
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	Downloads   int64  `json:"downloads"`
	Author      string `json:"author"`
	ProjectType string `json:"project_type"`
}

// Key is the project's ID, whichever field holds it.
func (p Project) Key() string {
	if p.ProjectID != "" {
		return p.ProjectID
	}
	return p.ID
}

// Version is one of a project's versions.
type Version struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Name          string    `json:"name"`
	VersionNumber string    `json:"version_number"`
	GameVersions  []string  `json:"game_versions"`
	Loaders       []string  `json:"loaders"`
	Published     time.Time `json:"date_published"`
	Files         []File    `json:"files"`
}

// File is a version's file.
type File struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Primary  bool   `json:"primary"`
	Hashes   struct {
		Sha512 string `json:"sha512"`
	} `json:"hashes"`
}

// Main is the version's primary file (or its first).
func (v Version) Main() (File, bool) {
	for _, f := range v.Files {
		if f.Primary {
			return f, true
		}
	}
	if len(v.Files) > 0 {
		return v.Files[0], true
	}
	return File{}, false
}

// projectType is Modrinth's project type for an add-on kind.
func projectType(kind string) string {
	if kind == "plugins" {
		return "plugin"
	}
	return "mod"
}

func jsonList(v []string) string {
	b, _ := json.Marshal(v) //nolint:errchkjson // strings always encode
	return string(b)
}

// Search searches a kind's projects for loaders, and a game version if
// given.
func (c *Client) Search(ctx context.Context, kind string, loaders []string, gameVersion, query string, offset int) ([]Project, int, error) {
	facets := [][]string{{"project_type:" + projectType(kind)}}
	var cats []string
	for _, l := range loaders {
		cats = append(cats, "categories:"+l)
	}
	facets = append(facets, cats)
	if gameVersion != "" {
		facets = append(facets, []string{"versions:" + gameVersion})
	}
	f, err := json.Marshal(facets)
	if err != nil {
		return nil, 0, err
	}
	q := url.Values{"query": {query}, "facets": {string(f)}, "limit": {strconv.Itoa(PageSize)}, "offset": {strconv.Itoa(offset)}}
	if query == "" {
		q.Set("index", "downloads")
	}
	var out struct {
		Hits      []Project `json:"hits"`
		TotalHits int       `json:"total_hits"`
	}
	if err := c.do(ctx, "/search", q, nil, &out); err != nil {
		return nil, 0, err
	}
	return out.Hits, out.TotalHits, nil
}

// Versions lists a project's versions for loaders and a game version (if
// given), newest first.
func (c *Client) Versions(ctx context.Context, project string, loaders []string, gameVersion string) ([]Version, error) {
	q := url.Values{"loaders": {jsonList(loaders)}}
	if gameVersion != "" {
		q.Set("game_versions", jsonList([]string{gameVersion}))
	}
	var out []Version
	err := c.do(ctx, "/project/"+url.PathEscape(project)+"/version", q, nil, &out)
	return out, err
}

// ByHash returns the versions files belong to, by SHA-512.
func (c *Client) ByHash(ctx context.Context, hashes []string) (map[string]Version, error) {
	out := map[string]Version{}
	err := c.do(ctx, "/version_files", nil, map[string]any{"hashes": hashes, "algorithm": "sha512"}, &out)
	return out, err
}

// Latest returns the newest version for loaders and a game version of
// each file's project, by the file's SHA-512.
func (c *Client) Latest(ctx context.Context, hashes, loaders []string, gameVersion string) (map[string]Version, error) {
	body := map[string]any{"hashes": hashes, "algorithm": "sha512", "loaders": loaders}
	if gameVersion != "" {
		body["game_versions"] = []string{gameVersion}
	}
	out := map[string]Version{}
	err := c.do(ctx, "/version_files/update", nil, body, &out)
	return out, err
}

// Projects describes projects by ID.
func (c *Client) Projects(ctx context.Context, ids []string) ([]Project, error) {
	var out []Project
	err := c.do(ctx, "/projects", url.Values{"ids": {jsonList(ids)}}, nil, &out)
	return out, err
}
