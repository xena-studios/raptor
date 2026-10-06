// Package dns keeps node hostnames pointing at their nodes
// (docs/ARCHITECTURE.md#node-dns): n-<short-id>.<node domain> (raptornodes.net)
// has an A record for the node's IPv4 address and an AAAA record for its
// IPv6 one, DNS-only, with a short TTL.
package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// Provider writes DNS records.
type Provider interface {
	// Set makes name's record of type ("A" or "AAAA") point at addr, creating
	// it if needed; an invalid addr removes it.
	Set(ctx context.Context, name, typ string, addr netip.Addr) error
}

// TTL of node records: short, so a home connection's new address spreads
// quickly.
const TTL = 60

// Cloudflare is a Provider for a Cloudflare zone, through its API with a
// token scoped to editing that zone's DNS.
type Cloudflare struct {
	Token  string
	ZoneID string
	// API defaults to https://api.cloudflare.com/client/v4.
	API    string
	Client *http.Client
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
}

type cfResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (c *Cloudflare) call(ctx context.Context, method, path string, body, out any) error {
	api := c.API
	if api == "" {
		api = "https://api.cloudflare.com/client/v4"
	}
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, api+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var r cfResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("cloudflare %s %s: %s", method, path, resp.Status)
	}
	if !r.Success {
		if len(r.Errors) > 0 {
			return fmt.Errorf("cloudflare %s %s: %s (%d)", method, path, r.Errors[0].Message, r.Errors[0].Code)
		}
		return fmt.Errorf("cloudflare %s %s: %s", method, path, resp.Status)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// Set implements Provider.
func (c *Cloudflare) Set(ctx context.Context, name, typ string, addr netip.Addr) error {
	if typ != "A" && typ != "AAAA" {
		return errors.New("record type must be A or AAAA")
	}
	base := "/zones/" + url.PathEscape(c.ZoneID) + "/dns_records"
	var existing []cfRecord
	q := url.Values{"type": {typ}, "name": {name}}
	if err := c.call(ctx, http.MethodGet, base+"?"+q.Encode(), nil, &existing); err != nil {
		return err
	}
	if !addr.IsValid() {
		for _, r := range existing {
			if err := c.call(ctx, http.MethodDelete, base+"/"+url.PathEscape(r.ID), nil, nil); err != nil {
				return err
			}
		}
		return nil
	}
	rec := cfRecord{Type: typ, Name: name, Content: addr.String(), TTL: TTL, Proxied: false, Comment: "Raptor node"}
	if len(existing) == 0 {
		return c.call(ctx, http.MethodPost, base, rec, nil)
	}
	if err := c.call(ctx, http.MethodPut, base+"/"+url.PathEscape(existing[0].ID), rec, nil); err != nil {
		return err
	}
	// Duplicates (from an earlier race) would make the name round-robin.
	for _, r := range existing[1:] {
		if err := c.call(ctx, http.MethodDelete, base+"/"+url.PathEscape(r.ID), nil, nil); err != nil {
			return err
		}
	}
	return nil
}
