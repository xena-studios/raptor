package demo

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := f[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

// The allowed hosts' addresses are loaded once the firewall is: a load
// that fails (Wings still starting) is tried again soon, not given up.
func TestRunEgress(t *testing.T) {
	egressRetry = 10 * time.Millisecond
	r := fakeResolver{"api.modrinth.com": {netip.MustParseAddr("1.2.3.4")}, "cdn.modrinth.com": {netip.MustParseAddr("::ffff:1.2.3.4"), netip.MustParseAddr("5.6.7.8")}}
	var mu sync.Mutex
	calls := 0
	var got []netip.Addr
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunEgress(ctx, r, slog.New(slog.DiscardHandler), func(_ context.Context, allow []netip.Addr) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return errors.New("the firewall isn't loaded yet")
		}
		if calls == 2 {
			got = allow
			close(done)
		}
		return nil
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the allowed hosts were never loaded after the first try failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != netip.MustParseAddr("1.2.3.4") || got[1] != netip.MustParseAddr("5.6.7.8") {
		t.Errorf("allowed: %v", got)
	}
}
