package nodelink

import (
	"context"
	"net"
	"net/http"
	"time"
)

// FamilyClient is base (nil: http.DefaultClient) with its connections made
// over one address family only: network is "tcp4" or "tcp6". The Panel
// learns a node's address in each family from connections made this way
// (PurposeAddress), and raptor doctor asks for the IPv4 one the same way.
func FamilyClient(base *http.Client, network string) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	var t *http.Transport
	if bt, ok := base.Transport.(*http.Transport); ok {
		t = bt.Clone()
	} else {
		t = http.DefaultTransport.(*http.Transport).Clone()
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return d.DialContext(ctx, network, addr)
	}
	c := *base
	c.Transport = t
	return &c
}
