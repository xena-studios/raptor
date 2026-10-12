// Package webapi calls the outside APIs the Panel asks for add-ons
// (Modrinth, Hangar): JSON over https, with answers cached for a few
// minutes, since many servers ask the same things and the APIs limit each
// address.
package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Limits.
const (
	cacheTTL     = 5 * time.Minute
	cacheEntries = 2000
	maxBody      = 8 << 20
)

// Client calls one API.
type Client struct {
	// Base is the API's address.
	Base string
	// HTTP makes the requests (nil: a client with a 15 second timeout).
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time
	// NotFound is returned for a 404, and Unavailable (wrapping the cause)
	// for anything else that isn't a good answer.
	NotFound, Unavailable error

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	body []byte
	at   time.Time
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Client) unavailable(cause any) error {
	u := c.Unavailable
	if u == nil {
		u = errors.New("the service isn't answering")
	}
	if err, ok := cause.(error); ok {
		return fmt.Errorf("%w (%w)", u, err)
	}
	return fmt.Errorf("%w (%v)", u, cause)
}

// Do calls the API and decodes its JSON answer into out, from the cache
// when it's fresh. body, if set, is POSTed.
func (c *Client) Do(ctx context.Context, path string, query url.Values, body any, out any) error {
	u := c.Base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	key := u + "\n" + string(payload)
	c.mu.Lock()
	hit, ok := c.cache[key]
	c.mu.Unlock()
	if ok && c.now().Sub(hit.at) < cacheTTL {
		return json.Unmarshal(hit.body, out)
	}
	method := http.MethodGet
	var rd io.Reader
	if body != nil {
		method, rd = http.MethodPost, bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return c.unavailable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return c.unavailable(err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound && c.NotFound != nil:
		return c.NotFound
	case resp.StatusCode != http.StatusOK:
		return c.unavailable(resp.Status)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return c.unavailable(err)
	}
	c.mu.Lock()
	if c.cache == nil || len(c.cache) >= cacheEntries {
		c.cache = map[string]cached{}
	}
	c.cache[key] = cached{data, c.now()}
	c.mu.Unlock()
	return nil
}
