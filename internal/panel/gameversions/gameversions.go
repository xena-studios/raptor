// Package gameversions lists the versions a game's server software has,
// from each project's own API, for the version switcher (#420): PaperMC's
// (Paper, Folia, Velocity, Waterfall), Purpur's, Mojang's (vanilla), and
// Fabric's and Quilt's. Answers are cached (webapi).
package gameversions

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/panel/webapi"
)

// Version is one of a project's versions.
type Version struct {
	ID string
	// Stable: a release, not a snapshot, pre-release, or release candidate.
	Stable bool
}

// ErrUnavailable is a project's API not answering as it should.
var ErrUnavailable = errors.New("the version list isn't answering right now; try again in a minute")

// ErrUnknown is a source this package doesn't know.
var ErrUnknown = errors.New("unknown game software")

// Sources are what List takes.
var Sources = []string{"paper", "folia", "velocity", "waterfall", "purpur", "vanilla", "fabric", "quilt"}

// Bases are each API's address (tests replace them).
type Bases struct {
	PaperMC, Purpur, Mojang, Fabric, Quilt string
}

// DefaultBases are the real APIs.
var DefaultBases = Bases{
	PaperMC: "https://fill.papermc.io",
	Purpur:  "https://api.purpurmc.org",
	Mojang:  "https://piston-meta.mojang.com",
	Fabric:  "https://meta.fabricmc.net",
	Quilt:   "https://meta.quiltmc.org",
}

// Client lists versions.
type Client struct {
	Bases     *Bases
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time

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
	a := &webapi.Client{Base: base, HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now, Unavailable: ErrUnavailable, NotFound: ErrUnavailable}
	c.apis[base] = a
	return a
}

func (c *Client) bases() Bases {
	if c.Bases != nil {
		return *c.Bases
	}
	return DefaultBases
}

// unstable: a version name that isn't a release.
func unstable(v string) bool {
	l := strings.ToLower(v)
	return strings.Contains(l, "-rc") || strings.Contains(l, "-pre") || strings.Contains(l, "snapshot") || strings.Contains(l, "beta") || strings.Contains(l, "alpha")
}

// List is a source's versions, newest first.
func (c *Client) List(ctx context.Context, source string) ([]Version, error) {
	b := c.bases()
	switch source {
	case "paper", "folia", "velocity", "waterfall":
		var out struct {
			// Families, newest first, each newest first (JSON object order
			// is kept by decoding into a slice of pairs below).
			Versions orderedVersions `json:"versions"`
		}
		if err := c.api(b.PaperMC).Do(ctx, "/v3/projects/"+source, nil, nil, &out); err != nil {
			return nil, err
		}
		var list []Version
		for _, fam := range out.Versions {
			for _, v := range fam {
				list = append(list, Version{ID: v, Stable: !unstable(v)})
			}
		}
		return list, nil
	case "purpur":
		var out struct {
			Versions []string `json:"versions"`
		}
		if err := c.api(b.Purpur).Do(ctx, "/v2/purpur", nil, nil, &out); err != nil {
			return nil, err
		}
		var list []Version
		for _, v := range slices.Backward(out.Versions) {
			list = append(list, Version{ID: v, Stable: !unstable(v)})
		}
		return list, nil
	case "vanilla":
		var out struct {
			Versions []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"versions"`
		}
		if err := c.api(b.Mojang).Do(ctx, "/mc/game/version_manifest_v2.json", nil, nil, &out); err != nil {
			return nil, err
		}
		var list []Version
		for _, v := range out.Versions {
			if v.Type == "release" || v.Type == "snapshot" {
				list = append(list, Version{ID: v.ID, Stable: v.Type == "release"})
			}
		}
		return list, nil
	case "fabric", "quilt":
		base, path := b.Fabric, "/v2/versions/game"
		if source == "quilt" {
			base, path = b.Quilt, "/v3/versions/game"
		}
		var out []struct {
			Version string `json:"version"`
			Stable  bool   `json:"stable"`
		}
		if err := c.api(base).Do(ctx, path, nil, nil, &out); err != nil {
			return nil, err
		}
		list := make([]Version, 0, len(out))
		for _, v := range out {
			list = append(list, Version{ID: v.Version, Stable: v.Stable})
		}
		return list, nil
	}
	return nil, ErrUnknown
}
