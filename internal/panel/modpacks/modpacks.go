// Package modpacks finds FTB's and Technic's modpacks for the pack picker
// (#413), beside Modrinth's: search, a pack's details, and its versions.
// The modpack eggs' install scripts download the packs; this only names
// them. Answers are cached (webapi).
package modpacks

import (
	"cmp"
	"context"
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
)

// Defaults are the APIs.
const (
	FTBBase     = "https://api.feed-the-beast.com"
	TechnicBase = "https://api.technicpack.net"
	// PageSize is how many packs a search returns.
	PageSize = 20
)

// Errors.
var (
	ErrUnavailable = errors.New("the modpack site isn't answering right now; try again in a minute")
	ErrNotFound    = errors.New("there's no such modpack")
)

// Pack is a modpack, as the picker shows it.
type Pack struct {
	ID          string
	Slug        string
	Title       string
	Description string
	Icon        string
	Installs    int64
	URL         string
}

// Version is one of a pack's versions.
type Version struct {
	ID     string
	Name   string
	Stable bool
	When   time.Time
}

// Client calls FTB and Technic.
type Client struct {
	FTB, Technic string
	HTTP         *http.Client
	UserAgent    string
	Now          func() time.Time

	mu   sync.Mutex
	apis map[string]*webapi.Client
}

func (c *Client) api(base string) *webapi.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.apis == nil {
		c.apis = map[string]*webapi.Client{}
	}
	if a, ok := c.apis[base]; ok {
		return a
	}
	a := &webapi.Client{Base: base, HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now, NotFound: ErrNotFound, Unavailable: ErrUnavailable}
	c.apis[base] = a
	return a
}

type ftbPack struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Synopsis string `json:"synopsis"`
	Installs int64  `json:"installs"`
	Private  bool   `json:"private"`
	Art      []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"art"`
	Versions []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Type    string `json:"type"`
		Updated int64  `json:"updated"`
		Private bool   `json:"private"`
	} `json:"versions"`
}

func (p ftbPack) pack() Pack {
	out := Pack{
		ID: strconv.FormatInt(p.ID, 10), Slug: p.Slug, Title: p.Name, Description: p.Synopsis, Installs: p.Installs,
		URL: "https://www.feed-the-beast.com/modpacks/" + strconv.FormatInt(p.ID, 10),
	}
	for _, a := range p.Art {
		if a.Type == "square" && strings.HasPrefix(a.URL, "https://cdn.feed-the-beast.com/") {
			out.Icon = a.URL
		}
	}
	return out
}

func (c *Client) ftbPack(ctx context.Context, id string) (ftbPack, error) {
	var p ftbPack
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return p, ErrNotFound
	}
	err := c.api(cmp.Or(c.FTB, FTBBase)).Do(ctx, "/v1/modpacks/public/modpack/"+id, nil, nil, &p)
	if err == nil && p.Private {
		return p, ErrNotFound
	}
	return p, err
}

// searchFTB is FTB's packs matching query, or its most installed.
func (c *Client) searchFTB(ctx context.Context, query string) ([]Pack, error) {
	var ids struct {
		Packs []int64 `json:"packs"`
	}
	path := "/v1/modpacks/public/modpack/popular/installs/" + strconv.Itoa(PageSize)
	var q url.Values
	if query != "" {
		path, q = "/v1/modpacks/public/modpack/search/"+strconv.Itoa(PageSize), url.Values{"term": {query}}
	}
	if err := c.api(cmp.Or(c.FTB, FTBBase)).Do(ctx, path, q, nil, &ids); err != nil {
		return nil, err
	}
	var out []Pack
	for _, id := range ids.Packs {
		p, err := c.ftbPack(ctx, strconv.FormatInt(id, 10))
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, p.pack())
	}
	return out, nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

// searchTechnic is Technic's packs matching query (Technic has no list of
// the most installed, so an empty query finds nothing).
func (c *Client) searchTechnic(ctx context.Context, query string) ([]Pack, error) {
	if query == "" {
		return nil, nil
	}
	var res struct {
		Modpacks []struct {
			Name    string `json:"name"`
			Slug    string `json:"slug"`
			URL     string `json:"url"`
			IconURL string `json:"iconUrl"`
		} `json:"modpacks"`
	}
	if err := c.api(cmp.Or(c.Technic, TechnicBase)).Do(ctx, "/search", url.Values{"build": {"999"}, "q": {query}}, nil, &res); err != nil {
		return nil, err
	}
	var out []Pack
	for _, m := range res.Modpacks {
		if !slugRe.MatchString(m.Slug) || len(out) == PageSize {
			continue
		}
		p := Pack{ID: m.Slug, Slug: m.Slug, Title: m.Name, URL: m.URL}
		if strings.HasPrefix(m.IconURL, "https://cdn.technicpack.net/") {
			p.Icon = m.IconURL
		}
		out = append(out, p)
	}
	return out, nil
}

// Search finds a source's packs ("ftb" or "technic").
func (c *Client) Search(ctx context.Context, source, query string) ([]Pack, error) {
	query = strings.TrimSpace(query)
	switch source {
	case "ftb":
		return c.searchFTB(ctx, query)
	case "technic":
		return c.searchTechnic(ctx, query)
	}
	return nil, ErrNotFound
}

// Versions are a pack's versions, newest first. Technic's are its
// recommended and latest builds, which its egg takes by name.
func (c *Client) Versions(ctx context.Context, source, id string) ([]Version, error) {
	switch source {
	case "ftb":
		p, err := c.ftbPack(ctx, id)
		if err != nil {
			return nil, err
		}
		var out []Version
		for _, v := range p.Versions {
			if v.Private {
				continue
			}
			out = append(out, Version{ID: strconv.FormatInt(v.ID, 10), Name: v.Name, Stable: v.Type == "release", When: time.Unix(v.Updated, 0)})
		}
		slices.SortFunc(out, func(a, b Version) int { return b.When.Compare(a.When) })
		return out, nil
	case "technic":
		if !slugRe.MatchString(id) {
			return nil, ErrNotFound
		}
		var p struct {
			DisplayName string `json:"displayName"`
			Version     string `json:"version"`
			Minecraft   string `json:"minecraft"`
		}
		if err := c.api(cmp.Or(c.Technic, TechnicBase)).Do(ctx, "/modpack/"+id, url.Values{"build": {"999"}}, nil, &p); err != nil {
			return nil, err
		}
		if p.DisplayName == "" {
			return nil, ErrNotFound
		}
		name := "Recommended"
		if p.Version != "" {
			name += " (" + p.Version + ", Minecraft " + p.Minecraft + ")"
		}
		return []Version{{ID: "recommended", Name: name, Stable: true}, {ID: "latest", Name: "Latest build"}}, nil
	}
	return nil, ErrNotFound
}
