package docker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeImages struct {
	mu     sync.Mutex
	here   map[string]bool
	pulls  []string
	block  chan struct{} // pulls wait on it, if set
	failed bool
}

func (f *fakeImages) images(now *time.Time) *images {
	return &images{
		local: func(_ context.Context, ref string) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.here[ref]
		},
		pull: func(ctx context.Context, ref string) error {
			if f.block != nil {
				select {
				case <-f.block:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.pulls = append(f.pulls, ref)
			if f.failed {
				return errors.New("registry down")
			}
			f.here[ref] = true
			return nil
		},
		now:   func() time.Time { return *now },
		tried: map[string]time.Time{},
	}
}

func (f *fakeImages) pulled() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.pulls...)
}

func TestImagesForStart(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	f := &fakeImages{here: map[string]bool{}}
	im := f.images(&now)
	notices := 0
	pulling := func() { notices++ }

	// Not here: pulled first, with a notice.
	if err := im.forStart(ctx, "game:latest", false, pulling); err != nil {
		t.Fatal(err)
	}
	if p := f.pulled(); len(p) != 1 || notices != 1 {
		t.Fatalf("pulls %v, notices %d", p, notices)
	}
	// Here and just pulled: started at once, no refresh.
	if err := im.forStart(ctx, "game:latest", false, pulling); err != nil {
		t.Fatal(err)
	}
	im.running.Wait()
	if p := f.pulled(); len(p) != 1 || notices != 1 {
		t.Fatalf("pulls %v, notices %d after a start with the image here", p, notices)
	}
	// Hours later: started at once, refreshed in the background once.
	now = now.Add(refreshEvery + time.Minute)
	f.block = make(chan struct{})
	for range 3 {
		if err := im.forStart(ctx, "game:latest", false, pulling); err != nil {
			t.Fatal(err)
		}
	}
	if p := f.pulled(); len(p) != 1 {
		t.Fatalf("a start waited on the refresh: %v", p)
	}
	close(f.block)
	im.running.Wait()
	if p := f.pulled(); len(p) != 2 || notices != 1 {
		t.Fatalf("pulls %v, notices %d, want one background refresh", p, notices)
	}
	f.block = nil
	// Forced (after an install or an image change): pulled first.
	if err := im.forStart(ctx, "game:latest", true, pulling); err != nil {
		t.Fatal(err)
	}
	if p := f.pulled(); len(p) != 3 || notices != 2 {
		t.Fatalf("forced: pulls %v, notices %d", p, notices)
	}
	// Pinned to a digest: never refreshed.
	pinned := "game@sha256:abc"
	f.here[pinned] = true
	now = now.Add(24 * time.Hour)
	if err := im.forStart(ctx, pinned, false, pulling); err != nil {
		t.Fatal(err)
	}
	im.running.Wait()
	if p := f.pulled(); len(p) != 3 {
		t.Fatalf("a pinned image was refreshed: %v", p)
	}
	// A failed refresh leaves the local copy in use, and isn't retried
	// on every start.
	f.failed = true
	for range 2 {
		if err := im.forStart(ctx, "game:latest", false, pulling); err != nil {
			t.Fatal(err)
		}
		im.running.Wait()
	}
	if p := f.pulled(); len(p) != 4 {
		t.Fatalf("pulls %v, want one failed refresh", p)
	}
	// A pull a start waits on that fails fails the start.
	if err := im.forStart(ctx, "other:1", false, pulling); err == nil {
		t.Fatal("a missing image that couldn't be pulled started")
	}
}
