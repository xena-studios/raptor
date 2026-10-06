// Package nodelinktest has a TCP proxy for breaking node connections in
// tests the way networks do: cut (both sides see the connection close) or
// frozen (bytes stop flowing and nobody is told).
package nodelinktest

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
)

// Proxy forwards TCP connections to a target.
type Proxy struct {
	Addr string // listen address, host:port

	ln     net.Listener
	target string
	mu     sync.Mutex
	conns  []net.Conn
	frozen bool
	gate   *sync.Cond
}

// NewProxy listens on localhost and forwards to target until the test ends.
func NewProxy(t *testing.T, target string) *Proxy {
	p, err := Listen(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// Listen starts a proxy on localhost that forwards to target.
func Listen(target string) (*Proxy, error) {
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Proxy{Addr: ln.Addr().String(), ln: ln, target: target}
	p.gate = sync.NewCond(&p.mu)
	go p.serve()
	return p, nil
}

// Close stops the proxy and closes its connections.
func (p *Proxy) Close() {
	_ = p.ln.Close()
	p.Thaw()
	p.Cut()
}

func (p *Proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		u, err := new(net.Dialer).DialContext(context.Background(), "tcp", p.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.mu.Lock()
		p.conns = append(p.conns, c, u)
		p.mu.Unlock()
		go p.pipe(c, u)
		go p.pipe(u, c)
	}
}

func (p *Proxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		p.mu.Lock()
		for p.frozen {
			p.gate.Wait()
		}
		p.mu.Unlock()
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				break
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				_ = src.Close()
			}
			break
		}
	}
	_ = dst.Close()
}

// Cut closes every connection through the proxy.
func (p *Proxy) Cut() {
	p.mu.Lock()
	conns := p.conns
	p.conns = nil
	p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// Freeze stops forwarding without closing anything, like a dead route.
func (p *Proxy) Freeze() {
	p.mu.Lock()
	p.frozen = true
	p.mu.Unlock()
}

// Thaw resumes forwarding.
func (p *Proxy) Thaw() {
	p.mu.Lock()
	p.frozen = false
	p.gate.Broadcast()
	p.mu.Unlock()
}
