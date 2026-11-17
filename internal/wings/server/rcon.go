package server

import (
	"context"
	"errors"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strconv"
)

// RCONTarget is where a running server's remote console listens (#423).
type RCONTarget struct {
	// Protocol is "source" or "webrcon"; Game the Players tab's command
	// set (the egg's x-raptor.rcon.players, "" for none).
	Protocol, Game string
	Addr           string // host:port
	Password       string
}

var (
	// ErrNoRCON is a server whose egg declares no RCON.
	ErrNoRCON = errors.New("this server's game has no RCON in its egg")
	// ErrRCONOff is RCON turned off, or without a password.
	ErrRCONOff = errors.New("RCON is off: turn it on and set its password in Startup, then restart the server")
	// ErrNotRunning is a server that isn't running.
	ErrNotRunning = errors.New("the server isn't running")
)

// RCON is where a server's RCON is, from its egg and variables. Wings
// connects to the container's own address (the RCON port needn't be an
// allocation), or to the node for a server on the host's network.
func (m *Manager) RCON(ctx context.Context, id string) (RCONTarget, error) {
	srv, err := m.Get(ctx, id)
	if err != nil {
		return RCONTarget{}, err
	}
	e := srv.Egg()
	if e == nil || e.Raptor.RCON == nil {
		return RCONTarget{}, ErrNoRCON
	}
	r := e.Raptor.RCON
	vars := map[string]string{}
	for _, v := range e.Variables {
		vars[v.Env] = v.Default
	}
	maps.Copy(vars, srv.Variables)
	password := r.PasswordFor(vars)
	if password == "" {
		return RCONTarget{}, ErrRCONOff
	}
	port := r.PortFor(srv.Primary().Port, vars)
	if port == 0 {
		return RCONTarget{}, errors.New("the RCON port isn't a port number")
	}
	st, err := m.Status(id)
	if err != nil {
		return RCONTarget{}, err
	}
	if st.State != Running && st.State != Starting {
		return RCONTarget{}, ErrNotRunning
	}
	ip, err := m.containerIP(ctx, srv)
	if err != nil {
		return RCONTarget{}, err
	}
	return RCONTarget{Protocol: r.Protocol, Game: r.Players, Addr: net.JoinHostPort(ip, strconv.Itoa(port)), Password: password}, nil
}

func (m *Manager) containerIP(ctx context.Context, srv *Server) (string, error) {
	if srv.HostNetwork {
		if ip := srv.Primary().IP; ip != "" && ip != "0.0.0.0" && ip != "::" {
			return ip, nil
		}
		return "127.0.0.1", nil
	}
	st, err := m.o.Runtime.Inspect(ctx, containerName(srv.ID))
	if err != nil {
		return "", err
	}
	if !st.Running {
		return "", ErrNotRunning
	}
	return pickIP(st.Networks)
}

// pickIP is a container's IPv4 address: any network's reaches it from
// the node, the same one each time.
func pickIP(nets map[string]netip.Addr) (string, error) {
	for _, name := range slices.Sorted(maps.Keys(nets)) {
		if a := nets[name]; a.Is4() && !a.IsUnspecified() {
			return a.String(), nil
		}
	}
	return "", errors.New("the server's container has no address")
}
