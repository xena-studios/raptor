package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// DocsIndexPath is the docs site's list of pages (docs-site/src/pages/
	// pages.json.ts), built with it.
	DocsIndexPath = "/pages.json"
	// docsFresh is how long the list is kept before it's fetched again; the
	// docs change with releases.
	docsFresh = time.Hour
	// maxChoices is the most suggestions Discord shows.
	maxChoices = 25
)

// Page is a docs page, as the docs site lists it.
type Page struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Section     string `json:"section"`
	Path        string `json:"path"`
}

// docsIndex is the docs site's list of pages, as last fetched.
type docsIndex struct {
	mu       sync.Mutex
	pages    []Page
	fetched  time.Time
	fetching bool
}

func (b *Bot) docsURL() string {
	if b.DocsURL != "" {
		return strings.TrimRight(b.DocsURL, "/")
	}
	return "https://docs.raptorpanel.net"
}

// pages is the list of docs pages. It's fetched in the background once
// it's stale; the first time, an answer waits for it (briefly).
func (b *Bot) pages(ctx context.Context) []Page {
	d := &b.docs
	d.mu.Lock()
	pages, stale, first := d.pages, b.now().Sub(d.fetched) > docsFresh, d.pages == nil
	start := stale && !d.fetching
	if start {
		d.fetching = true
	}
	d.mu.Unlock()
	if !stale {
		return pages
	}
	done := make(chan struct{})
	if start {
		go func() {
			defer close(done)
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			fresh, err := b.fetchPages(fctx)
			d.mu.Lock()
			defer d.mu.Unlock()
			d.fetching = false
			if err != nil {
				b.log().Warn("discord: fetching the docs' pages failed", "err", err)
				return
			}
			d.pages, d.fetched = fresh, b.now()
		}()
	}
	if first && start {
		select {
		case <-done:
		case <-ctx.Done():
		}
		d.mu.Lock()
		pages = d.pages
		d.mu.Unlock()
	}
	return pages
}

func (b *Bot) fetchPages(ctx context.Context) ([]Page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.docsURL()+DocsIndexPath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", DocsIndexPath, resp.Status)
	}
	var pages []Page
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&pages); err != nil {
		return nil, err
	}
	return pages, nil
}

// search ranks pages for a query: words in the title count most, then the
// section, then the description. An empty query lists the first pages.
func search(pages []Page, query string) []Page {
	words := strings.Fields(strings.ToLower(query))
	type hit struct {
		p     Page
		score int
		i     int
	}
	var hits []hit
	for i, p := range pages {
		title, section, desc := strings.ToLower(p.Title), strings.ToLower(p.Section), strings.ToLower(p.Description)
		score := 0
		for _, w := range words {
			switch {
			case strings.HasPrefix(title, w) || strings.Contains(title, " "+w):
				score += 6
			case strings.Contains(title, w):
				score += 4
			case strings.Contains(section, w):
				score += 2
			case strings.Contains(desc, w) || strings.Contains(p.Path, w):
				score++
			default:
				// Every word has to be somewhere.
				score = -1
			}
			if score < 0 {
				break
			}
		}
		if score >= 0 {
			hits = append(hits, hit{p, score, i})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return b.score - a.score })
	out := make([]Page, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.p)
	}
	return out
}

type choice struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// docsChoices are the suggestions as someone types /docs.
func (b *Bot) docsChoices(ctx context.Context, query string) []choice {
	out := []choice{}
	for _, p := range search(b.pages(ctx), query) {
		if len(out) == maxChoices {
			break
		}
		name := p.Title
		if p.Section != "" {
			name = p.Section + " › " + p.Title
		}
		out = append(out, choice{Name: clip(name, 100), Value: clip(p.Path, 100)})
	}
	return out
}

// docsCommand answers /docs: the page picked from the suggestions, or the
// best match for what was typed, for everyone in the channel to see.
func (b *Bot) docsCommand(ctx context.Context, in *interaction) answer {
	query := in.option("page")
	pages := b.pages(ctx)
	var found *Page
	for i := range pages {
		if pages[i].Path == query {
			found = &pages[i]
			break
		}
	}
	if found == nil {
		if hits := search(pages, query); len(hits) > 0 {
			found = &hits[0]
		}
	}
	if found == nil {
		return say(fmt.Sprintf("No page matches %q. Search the whole guide at %s.", clip(query, 80), b.docsURL()))
	}
	e := embed{Title: found.Title, URL: b.docsURL() + found.Path, Description: found.Description, Color: brandColor}
	if found.Section != "" {
		e.Footer = &footer{Text: "Raptor docs · " + found.Section}
	}
	return reply(message{Embeds: []embed{e}}, false)
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
