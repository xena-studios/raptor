// Package reforger is the Panel's client for the Arma Reforger Workshop
// (reforger.armaplatform.com, #417): what a mod is by its ID (name, author,
// newest version, the mods it needs, its scenarios), and search. Bohemia
// has no documented API, so the client reads the data the Workshop's own
// pages carry (their __NEXT_DATA__ JSON). If that ever changes, lookups
// fail and the app still takes mod IDs and names typed in. Answers are
// cached (webapi).
package reforger

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
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

// DefaultBase is the Workshop's site.
const DefaultBase = "https://reforger.armaplatform.com"

// PreviewHost is where mods' pictures are: the app's CSP allows images
// from it, so pictures anywhere else are left out.
const PreviewHost = "ar-gcp-cdn.bistudio.com"

// Limits.
const (
	// MaxMods is how many mods one lookup takes.
	MaxMods = 100
	// lookups is how many pages one lookup fetches at once.
	lookups = 6
)

// Errors.
var (
	ErrUnavailable = errors.New("the Arma Reforger Workshop isn't answering right now; try again in a minute")
	ErrNotFound    = errors.New("the Arma Reforger Workshop doesn't have that mod")
)

// ID is a mod's ID: 16 hexadecimal digits, as in its Workshop link.
var ID = regexp.MustCompile(`^[0-9A-F]{16}$`)

// Client calls the Workshop.
type Client struct {
	// Base is the Workshop's address (empty: DefaultBase).
	Base string
	// HTTP makes the requests (nil: a client with a 15 second timeout).
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time

	once sync.Once
	api  *webapi.Client
}

// Mod is a Workshop mod.
type Mod struct {
	ID      string
	Name    string
	Author  string
	Summary string
	// Version is its newest version.
	Version string
	// Size is the newest version's, in bytes.
	Size    int64
	Preview string
	Updated time.Time
	// Dependencies are the mods it needs (empty from a search).
	Dependencies []Ref
	// Scenarios are the scenarios it adds (empty from a search).
	Scenarios []Scenario
}

// Ref names another mod.
type Ref struct{ ID, Name string }

// Scenario is a scenario a mod adds: ID is what the server's
// game.scenarioId takes.
type Scenario struct{ ID, Name string }

// page is what the Workshop's pages carry: a mod's, or a search's.
type page struct {
	Asset  *asset `json:"asset"`
	Assets *struct {
		Count int     `json:"count"`
		Rows  []asset `json:"rows"`
	} `json:"assets"`
}

type asset struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	Type                 string    `json:"type"`
	Summary              string    `json:"summary"`
	Private              bool      `json:"private"`
	Blocked              bool      `json:"blocked"`
	CurrentVersionNumber string    `json:"currentVersionNumber"`
	CurrentVersionSize   int64     `json:"currentVersionSize"`
	UpdatedAt            time.Time `json:"updatedAt"`
	Author               *struct {
		Username string `json:"username"`
	} `json:"author"`
	Previews []struct {
		URL        string `json:"url"`
		Thumbnails map[string][]struct {
			URL   string `json:"url"`
			Width int    `json:"width"`
		} `json:"thumbnails"`
	} `json:"previews"`
	Dependencies []struct {
		Asset struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"asset"`
	} `json:"dependencies"`
	Scenarios []struct {
		Name   string `json:"name"`
		GameID string `json:"gameId"`
	} `json:"scenarios"`
}

var nextData = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)

// extract is a Workshop page's data (its props.pageProps).
func extract(html []byte) ([]byte, error) {
	m := nextData.FindSubmatch(html)
	if m == nil {
		return nil, errors.New("no page data")
	}
	var d struct {
		Props struct {
			PageProps json.RawMessage `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(m[1]), &d); err != nil {
		return nil, err
	}
	if len(d.Props.PageProps) == 0 {
		return nil, errors.New("no page data")
	}
	return d.Props.PageProps, nil
}

func (c *Client) do(ctx context.Context, path string, query url.Values, out any) error {
	c.once.Do(func() {
		c.api = &webapi.Client{
			Base: cmp.Or(c.Base, DefaultBase), HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now,
			NotFound: ErrNotFound, Unavailable: ErrUnavailable, Extract: extract,
		}
	})
	return c.api.Do(ctx, path, query, nil, out)
}

// Get is a mod by its ID. A private or blocked mod is ErrNotFound: the
// server can't download it either.
func (c *Client) Get(ctx context.Context, id string) (Mod, error) {
	if !ID.MatchString(id) {
		return Mod{}, ErrNotFound
	}
	var p page
	if err := c.do(ctx, "/workshop/"+id, nil, &p); err != nil {
		return Mod{}, err
	}
	if p.Asset == nil || !strings.EqualFold(p.Asset.ID, id) || p.Asset.Private || p.Asset.Blocked {
		return Mod{}, ErrNotFound
	}
	return p.Asset.mod(), nil
}

// Lookup is the mods with these IDs, by ID; ones the Workshop doesn't have
// are left out. It fails only if the Workshop isn't answering at all.
func (c *Client) Lookup(ctx context.Context, ids []string) (map[string]Mod, error) {
	out := map[string]Mod{}
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		failed  error
		answers int
	)
	sem := make(chan struct{}, lookups)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := c.Get(ctx, id)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				out[id] = m
				answers++
			case errors.Is(err, ErrNotFound):
				answers++
			default:
				failed = err
			}
		}()
	}
	wg.Wait()
	if answers == 0 && failed != nil {
		return nil, failed
	}
	return out, nil
}

// Search is a page (from 1) of the mods matching query, as the Workshop
// orders them, and how many match in all.
func (c *Client) Search(ctx context.Context, query string, pg int) ([]Mod, int, error) {
	q := url.Values{"search": {query}}
	if pg > 1 {
		q.Set("page", strconv.Itoa(pg))
	}
	var p page
	if err := c.do(ctx, "/workshop", q, &p); err != nil {
		return nil, 0, err
	}
	if p.Assets == nil {
		return nil, 0, ErrUnavailable
	}
	var out []Mod
	for _, a := range p.Assets.Rows {
		if a.Private || a.Blocked || !ID.MatchString(a.ID) {
			continue
		}
		out = append(out, a.mod())
	}
	return out, p.Assets.Count, nil
}

func (a *asset) mod() Mod {
	m := Mod{
		ID: a.ID, Name: a.Name, Summary: a.Summary, Version: a.CurrentVersionNumber,
		Size: a.CurrentVersionSize, Updated: a.UpdatedAt, Preview: a.preview(),
	}
	if a.Author != nil {
		m.Author = a.Author.Username
	}
	for _, d := range a.Dependencies {
		if ID.MatchString(d.Asset.ID) {
			m.Dependencies = append(m.Dependencies, Ref{ID: d.Asset.ID, Name: d.Asset.Name})
		}
	}
	for _, s := range a.Scenarios {
		if s.GameID != "" {
			m.Scenarios = append(m.Scenarios, Scenario{ID: s.GameID, Name: s.Name})
		}
	}
	return m
}

// preview is the mod's picture: its first preview's smallest thumbnail at
// least 288 wide (the Workshop's list size), or the preview itself; ""
// unless it's on PreviewHost.
func (a *asset) preview() string {
	if len(a.Previews) == 0 {
		return ""
	}
	p := a.Previews[0]
	best, width := p.URL, 0
	for _, t := range p.Thumbnails["image/jpeg"] {
		if t.Width >= 288 && (width == 0 || t.Width < width) {
			best, width = t.URL, t.Width
		}
	}
	u, err := url.Parse(best)
	if err != nil || u.Scheme != "https" || u.Host != PreviewHost {
		return ""
	}
	return best
}
