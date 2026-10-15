package alerts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// errPrivate is a channel URL that reaches somewhere other than the public
// internet: the Panel's own network, the WireGuard link to the other
// server, a cloud metadata address. Alerts must never be a way to make the
// Panel send requests there.
var errPrivate = errors.New("that address isn't on the public internet")

// cgnat is shared address space (RFC 6598), not public.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Public reports whether an address is a public unicast one.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a) &&
		(!a.Is4() || a.As4()[0] != 0) // not 0.0.0.0/8
}

// safeClient makes HTTP clients that only connect to public addresses,
// checked on every connection (not when the URL was saved, since DNS can
// change after), don't follow redirects, and use no proxy.
func safeClient(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w (%s)", errPrivate, address)
			}
			if !Public(ap.Addr()) {
				return errPrivate
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return d.DialContext(ctx, network, addr)
			},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			MaxIdleConns:          20,
			IdleConnTimeout:       time.Minute,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
