// Package mclogs shares a server's log on mclo.gs, the paste site Minecraft
// players and plugin authors use for support (#429): it highlights errors
// and gives a link to send. The owner chooses to share each time; the
// Panel hides IP addresses and node hostnames first.
package mclogs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultBase is mclo.gs' API.
const DefaultBase = "https://api.mclo.gs"

// MaxBytes is the most of a log shared: mclo.gs takes 10 MiB, and the
// Panel's API takes 2 MiB a request, so the app sends the newest part.
const MaxBytes = 1 << 20

// ErrUnavailable is mclo.gs not answering as it should.
var ErrUnavailable = errors.New("mclo.gs isn't answering right now; try again in a minute")

// Client shares logs.
type Client struct {
	// Base is the API's address (empty: DefaultBase).
	Base string
	// HTTP makes the requests (nil: a client with a 20 second timeout).
	HTTP      *http.Client
	UserAgent string
}

// Share posts a log and returns its link.
func (c *Client) Share(ctx context.Context, content string) (string, error) {
	base := c.Base
	if base == "" {
		base = DefaultBase
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	form := url.Values{"content": {content}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/1/log", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return "", ErrUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Success bool   `json:"success"`
		URL     string `json:"url"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return "", ErrUnavailable
	}
	if !out.Success {
		if out.Error != "" && resp.StatusCode < 500 {
			return "", errors.New("mclo.gs: " + out.Error)
		}
		return "", ErrUnavailable
	}
	// Only a link to mclo.gs itself goes back to the app.
	u, err := url.Parse(out.URL)
	if err != nil || u.Scheme != "https" || (u.Host != "mclo.gs" && !strings.HasSuffix(u.Host, ".mclo.gs")) {
		return "", ErrUnavailable
	}
	return u.String(), nil
}

// Hidden replaces what Redact hides.
const Hidden = "[hidden]"

var (
	ipv4      = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	ipv6      = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}`)
	nodeNames = regexp.MustCompile(`(?i)\b[a-z0-9-]+\.raptornodes\.net\b`)
)

// Redact hides IP addresses and node hostnames (which lead to the
// machine) in a log, keeping the ones that only say how the game listens
// (0.0.0.0, 127.0.0.1, ::, ::1), which help whoever reads it.
func Redact(s string) string {
	s = nodeNames.ReplaceAllString(s, Hidden)
	s = ipv4.ReplaceAllStringFunc(s, func(m string) string {
		a, err := netip.ParseAddr(m)
		if err != nil || a.IsUnspecified() || a.IsLoopback() {
			return m
		}
		return Hidden
	})
	return ipv6.ReplaceAllStringFunc(s, func(m string) string {
		a, err := netip.ParseAddr(m)
		if err != nil || !a.Is6() || a.IsUnspecified() || a.IsLoopback() {
			return m
		}
		return Hidden
	})
}
