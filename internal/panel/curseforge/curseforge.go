// Package curseforge is the Panel's client for CurseForge's API
// (https://docs.curseforge.com/rest-api/), for Minecraft mods and Bukkit
// plugins (#412). It needs an API key from the CurseForge for Studios
// console, which stays on the Panel; without one CurseForge is off.
// Answers are cached (webapi).
package curseforge

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

// DefaultBase is CurseForge's API.
const DefaultBase = "https://api.curseforge.com"

// Minecraft's game and classes on CurseForge.
const (
	GameMinecraft = 432
	ClassMods     = 6
	ClassPlugins  = 5 // Bukkit plugins
)

// Limits.
const (
	// PageSize is how many projects a search returns.
	PageSize = 20
	// MaxFingerprints is how many files one lookup takes.
	MaxFingerprints = 500
	// maxIndex is the furthest CurseForge pages (index + pageSize).
	maxIndex     = 10000
	filesPerPage = 25
)

// Errors.
var (
	ErrUnavailable = errors.New("curseforge isn't answering right now; try again in a minute")
	ErrNotFound    = errors.New("curseforge doesn't have that")
)

// Client calls CurseForge.
type Client struct {
	// Key is the API key.
	Key string
	// Base is the API's address (empty: DefaultBase).
	Base string
	// HTTP makes the requests (nil: a client with a 15 second timeout).
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time

	once sync.Once
	api  *webapi.Client
}

// On reports whether CurseForge can be used: it has a key.
func (c *Client) On() bool { return c != nil && c.Key != "" }

func (c *Client) do(ctx context.Context, path string, query url.Values, body, out any) error {
	c.once.Do(func() {
		base := c.Base
		if base == "" {
			base = DefaultBase
		}
		c.api = &webapi.Client{
			Base: base, HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now,
			Header:   http.Header{"X-Api-Key": {c.Key}},
			NotFound: ErrNotFound, Unavailable: ErrUnavailable,
		}
	})
	return c.api.Do(ctx, path, query, body, out)
}

// LoaderType is CurseForge's modLoaderType for a loader name, or 0.
func LoaderType(loader string) int {
	switch loader {
	case "forge":
		return 1
	case "fabric":
		return 4
	case "quilt":
		return 5
	case "neoforge":
		return 6
	}
	return 0
}

// loaderNames are the names CurseForge puts among a file's game versions.
var loaderNames = map[string]string{"forge": "forge", "fabric": "fabric", "quilt": "quilt", "neoforge": "neoforge"}

// Mod is a CurseForge project.
type Mod struct {
	ID            int64  `json:"id"`
	ClassID       int64  `json:"classId"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Summary       string `json:"summary"`
	DownloadCount int64  `json:"downloadCount"`
	Links         struct {
		WebsiteURL string `json:"websiteUrl"`
	} `json:"links"`
	Logo *struct {
		ThumbnailURL string `json:"thumbnailUrl"`
	} `json:"logo"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
	// AllowModDistribution false: the author turned off downloads outside
	// CurseForge's own app, so files must be downloaded by hand.
	AllowModDistribution *bool `json:"allowModDistribution"`
}

// Distributable reports whether the mod's files may be downloaded by
// other apps.
func (m Mod) Distributable() bool { return m.AllowModDistribution == nil || *m.AllowModDistribution }

// Author is the first author's name.
func (m Mod) Author() string {
	if len(m.Authors) == 0 {
		return ""
	}
	return m.Authors[0].Name
}

// IconHost is where mods' icons are.
const IconHost = "media.forgecdn.net"

// Icon is the mod's icon, if it's on CurseForge's media host.
func (m Mod) Icon() string {
	if m.Logo == nil {
		return ""
	}
	if u, err := url.Parse(m.Logo.ThumbnailURL); err == nil && u.Scheme == "https" && u.Host == IconHost {
		return m.Logo.ThumbnailURL
	}
	return ""
}

// Page is the mod's page on curseforge.com.
func (m Mod) Page() string {
	if strings.HasPrefix(m.Links.WebsiteURL, "https://www.curseforge.com/") {
		return m.Links.WebsiteURL
	}
	kind := "mc-mods"
	if m.ClassID == ClassPlugins {
		kind = "bukkit-plugins"
	}
	return "https://www.curseforge.com/minecraft/" + kind + "/" + url.PathEscape(m.Slug)
}

// File is one of a mod's files.
type File struct {
	ID           int64     `json:"id"`
	ModID        int64     `json:"modId"`
	DisplayName  string    `json:"displayName"`
	FileName     string    `json:"fileName"`
	FileDate     time.Time `json:"fileDate"`
	FileLength   int64     `json:"fileLength"`
	DownloadURL  string    `json:"downloadUrl"`
	IsAvailable  bool      `json:"isAvailable"`
	GameVersions []string  `json:"gameVersions"`
	Hashes       []struct {
		Value string `json:"value"`
		Algo  int    `json:"algo"` // 1 SHA-1, 2 MD5
	} `json:"hashes"`
	Dependencies []struct {
		ModID        int64 `json:"modId"`
		RelationType int   `json:"relationType"`
	} `json:"dependencies"`
	Fingerprint uint32 `json:"fileFingerprint"`
}

// SHA1 is the file's SHA-1, if listed.
func (f File) SHA1() string {
	for _, h := range f.Hashes {
		if h.Algo == 1 && len(h.Value) == 40 {
			return strings.ToLower(h.Value)
		}
	}
	return ""
}

// DownloadHosts are where CurseForge's files are.
var DownloadHosts = []string{"edge.forgecdn.net", "mediafilez.forgecdn.net"}

// Installable reports whether Wings can download the file: it has a URL on
// CurseForge's CDN and a SHA-1.
func (f File) Installable() bool {
	u, err := url.Parse(f.DownloadURL)
	return err == nil && u.Scheme == "https" && slices.Contains(DownloadHosts, u.Host) && f.SHA1() != ""
}

// Versions are the game versions in the file's list, without the loaders
// and sides CurseForge puts there too.
func (f File) Versions() []string {
	var out []string
	for _, v := range f.GameVersions {
		if v != "" && v[0] >= '0' && v[0] <= '9' {
			out = append(out, v)
		}
	}
	return out
}

// Loaders are the loaders in the file's list.
func (f File) Loaders() []string {
	var out []string
	for _, v := range f.GameVersions {
		if l, ok := loaderNames[strings.ToLower(v)]; ok {
			out = append(out, l)
		}
	}
	return out
}

// Relation is a dependency's kind, as the other add-on sources name them.
func Relation(t int) string {
	switch t {
	case 3:
		return "required"
	case 2:
		return "optional"
	case 5:
		return "incompatible"
	}
	return ""
}

// Search searches a class's projects for a loader (mods) and game version,
// most popular first unless there's a query.
func (c *Client) Search(ctx context.Context, class int, loader, gameVersion, query string, offset int) ([]Mod, int, error) {
	if offset+PageSize > maxIndex {
		return nil, maxIndex, nil
	}
	q := url.Values{
		"gameId": {strconv.Itoa(GameMinecraft)}, "classId": {strconv.Itoa(class)},
		"index": {strconv.Itoa(offset)}, "pageSize": {strconv.Itoa(PageSize)},
		"sortField": {"2"}, "sortOrder": {"desc"},
	}
	if t := LoaderType(loader); t != 0 && class == ClassMods {
		q.Set("modLoaderType", strconv.Itoa(t))
	}
	if gameVersion != "" {
		q.Set("gameVersion", gameVersion)
	}
	if query != "" {
		q.Set("searchFilter", query)
	}
	var out struct {
		Data       []Mod `json:"data"`
		Pagination struct {
			TotalCount int `json:"totalCount"`
		} `json:"pagination"`
	}
	if err := c.do(ctx, "/v1/mods/search", q, nil, &out); err != nil {
		return nil, 0, err
	}
	return out.Data, min(out.Pagination.TotalCount, maxIndex), nil
}

// Mod is a project by ID.
func (c *Client) Mod(ctx context.Context, id int64) (Mod, error) {
	var out struct {
		Data Mod `json:"data"`
	}
	err := c.do(ctx, "/v1/mods/"+strconv.FormatInt(id, 10), nil, nil, &out)
	return out.Data, err
}

// Mods are projects by ID.
func (c *Client) Mods(ctx context.Context, ids []int64) ([]Mod, error) {
	var out struct {
		Data []Mod `json:"data"`
	}
	err := c.do(ctx, "/v1/mods", nil, map[string]any{"modIds": ids, "filterPcOnly": true}, &out)
	return out.Data, err
}

// Files lists a project's files for a loader (mods) and a game version,
// newest first, leaving out ones that aren't available.
func (c *Client) Files(ctx context.Context, mod int64, loader, gameVersion string) ([]File, error) {
	q := url.Values{"pageSize": {strconv.Itoa(filesPerPage)}}
	if t := LoaderType(loader); t != 0 {
		q.Set("modLoaderType", strconv.Itoa(t))
	}
	if gameVersion != "" {
		q.Set("gameVersion", gameVersion)
	}
	var out struct {
		Data []File `json:"data"`
	}
	if err := c.do(ctx, "/v1/mods/"+strconv.FormatInt(mod, 10)+"/files", q, nil, &out); err != nil {
		return nil, err
	}
	files := slices.DeleteFunc(out.Data, func(f File) bool { return !f.IsAvailable })
	slices.SortStableFunc(files, func(a, b File) int { return b.FileDate.Compare(a.FileDate) })
	return files, nil
}

// File is one file of a project.
func (c *Client) File(ctx context.Context, mod, file int64) (File, error) {
	var out struct {
		Data File `json:"data"`
	}
	err := c.do(ctx, "/v1/mods/"+strconv.FormatInt(mod, 10)+"/files/"+strconv.FormatInt(file, 10), nil, nil, &out)
	return out.Data, err
}

// ByFingerprint looks files up by their fingerprints: the files CurseForge
// has, by fingerprint.
func (c *Client) ByFingerprint(ctx context.Context, fingerprints []uint32) (map[uint32]File, error) {
	var out struct {
		Data struct {
			ExactMatches []struct {
				File File `json:"file"`
			} `json:"exactMatches"`
		} `json:"data"`
	}
	err := c.do(ctx, "/v1/fingerprints/"+strconv.Itoa(GameMinecraft), nil, map[string]any{"fingerprints": fingerprints}, &out)
	if err != nil {
		return nil, err
	}
	files := map[uint32]File{}
	for _, m := range out.Data.ExactMatches {
		files[m.File.Fingerprint] = m.File
	}
	return files, nil
}
