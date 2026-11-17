package docker

import (
	"context"
	"strings"
	"sync"
	"time"
)

// A server start doesn't wait on the registry when its image is already
// here (docs/DECISIONS.md #386): it starts from the local copy, and the
// tag is pulled again in the background. The trade-off: a mutable tag
// (":latest", ":java21") can run an image a little behind the registry
// until that refresh lands; the next start uses the new one.
const (
	// refreshEvery is how often one ref is refreshed in the background,
	// however often servers using it start.
	refreshEvery = 3 * time.Hour
	// refreshTimeout bounds a background refresh. Layers it got are kept,
	// so the next one carries on.
	refreshTimeout = 60 * time.Second
)

// images decides when a start pulls its image.
type images struct {
	local func(ctx context.Context, ref string) bool
	pull  func(ctx context.Context, ref string) error
	now   func() time.Time

	mu      sync.Mutex
	tried   map[string]time.Time // when each ref was last pulled or refreshed
	running sync.WaitGroup       // background refreshes, for tests
}

// forStart makes sure ref is here for a server to start from. force pulls
// first even when it is (the first start after an install, or after the
// image changed); so does an image that isn't here. pulling, if set, is
// called before a pull the start waits on.
func (im *images) forStart(ctx context.Context, ref string, force bool, pulling func()) error {
	if !force && im.local(ctx, ref) {
		im.refresh(ctx, ref)
		return nil
	}
	if pulling != nil {
		pulling()
	}
	if err := im.pull(ctx, ref); err != nil {
		return err
	}
	im.mu.Lock()
	im.tried[ref] = im.now()
	im.mu.Unlock()
	return nil
}

// refresh pulls ref in the background, unless it's pinned to a digest
// (which can't change), or it was pulled or refreshed lately, or a
// refresh is already under way (it counts as tried from when it starts).
func (im *images) refresh(ctx context.Context, ref string) {
	if strings.Contains(ref, "@") {
		return
	}
	now := im.now()
	im.mu.Lock()
	if last, ok := im.tried[ref]; ok && now.Sub(last) < refreshEvery {
		im.mu.Unlock()
		return
	}
	im.tried[ref] = now
	im.mu.Unlock()
	im.running.Go(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		defer cancel()
		// A failed refresh just leaves the local copy in use; it's tried
		// again after refreshEvery.
		_ = im.pull(ctx, ref)
	})
}
