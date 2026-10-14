// Package steam is the Panel's client for Steam's public Workshop API
// (ISteamRemoteStorage, no key needed), for the Workshop picker
// (docs/EGGS.md#steam-workshop): what an item or collection is, so the app
// can show names instead of numbers. Answers are cached (webapi).
package steam

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/panel/webapi"
)

// DefaultBase is Steam's Web API.
const DefaultBase = "https://api.steampowered.com"

// MaxItems is how many items one lookup takes.
const MaxItems = 100

// PreviewHost is where item previews may come from: the app's CSP allows
// images from it, so previews anywhere else are left out.
const PreviewHost = "images.steamusercontent.com"

// ErrUnavailable is Steam not answering as it should.
var ErrUnavailable = errors.New("steam isn't answering right now; try again in a minute")

// Client calls Steam.
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

func (c *Client) do(ctx context.Context, path string, form url.Values, out any) error {
	c.once.Do(func() {
		base := c.Base
		if base == "" {
			base = DefaultBase
		}
		c.api = &webapi.Client{Base: base, HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now, Unavailable: ErrUnavailable}
	})
	return c.api.Do(ctx, path, nil, form, out)
}

// Item is a Workshop item or collection.
type Item struct {
	ID    string
	AppID int64 // the game it's for
	Title string
	// Preview is its image, if it's on PreviewHost.
	Preview string
	Size    int64 // bytes; 0 for collections
	Updated time.Time
	// Children are a collection's items; nil for an item.
	Children   []string
	Collection bool
}

// steam's result codes: 1 is OK; anything else (9 is "not found") means
// the item doesn't exist, isn't public, or isn't a collection.
const resultOK = 1

type detailsResponse struct {
	Response struct {
		Details []struct {
			ID         string `json:"publishedfileid"`
			Result     int    `json:"result"`
			AppID      int64  `json:"consumer_app_id"`
			Title      string `json:"title"`
			Preview    string `json:"preview_url"`
			Size       string `json:"file_size"`
			Updated    int64  `json:"time_updated"`
			Banned     int    `json:"banned"`
			Visibility int    `json:"visibility"` // 0 is public
		} `json:"publishedfiledetails"`
	} `json:"response"`
}

type collectionResponse struct {
	Response struct {
		Details []struct {
			ID       string `json:"publishedfileid"`
			Result   int    `json:"result"`
			Children []struct {
				ID string `json:"publishedfileid"`
			} `json:"children"`
		} `json:"collectiondetails"`
	} `json:"response"`
}

func idsForm(countKey string, ids []string) url.Values {
	f := url.Values{countKey: {strconv.Itoa(len(ids))}}
	for i, id := range ids {
		f.Set("publishedfileids["+strconv.Itoa(i)+"]", id)
	}
	return f
}

// Items looks items and collections up by ID. IDs Steam doesn't have, or
// that aren't public, are left out.
func (c *Client) Items(ctx context.Context, ids []string) (map[string]Item, error) {
	if len(ids) == 0 {
		return map[string]Item{}, nil
	}
	if len(ids) > MaxItems {
		return nil, errors.New("too many items")
	}
	var d detailsResponse
	if err := c.do(ctx, "/ISteamRemoteStorage/GetPublishedFileDetails/v1/", idsForm("itemcount", ids), &d); err != nil {
		return nil, err
	}
	var col collectionResponse
	if err := c.do(ctx, "/ISteamRemoteStorage/GetCollectionDetails/v1/", idsForm("collectioncount", ids), &col); err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for _, cd := range col.Response.Details {
		if cd.Result != resultOK {
			continue
		}
		list := []string{}
		for _, ch := range cd.Children {
			list = append(list, ch.ID)
		}
		children[cd.ID] = list
	}
	out := map[string]Item{}
	for _, it := range d.Response.Details {
		if it.Result != resultOK || it.Banned != 0 || it.Visibility != 0 {
			continue
		}
		item := Item{ID: it.ID, AppID: it.AppID, Title: it.Title, Updated: time.Unix(it.Updated, 0).UTC()}
		if u, err := url.Parse(it.Preview); err == nil && u.Scheme == "https" && u.Host == PreviewHost {
			item.Preview = it.Preview
		}
		if ch, ok := children[it.ID]; ok {
			item.Collection, item.Children = true, ch
		} else {
			item.Size, _ = strconv.ParseInt(it.Size, 10, 64)
		}
		out[it.ID] = item
	}
	return out, nil
}
