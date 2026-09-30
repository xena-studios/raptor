package server

import (
	"context"
	"fmt"
	"strings"
)

// shortIDLen is the length of a server's short ID: the first characters of
// its UUID, as Pterodactyl uses in SFTP usernames.
const shortIDLen = 8

// Resolve returns the ID of the server ref names: its full ID, or its short
// ID when exactly one server has it.
func (m *Manager) Resolve(ref string) (string, error) {
	ref = strings.ToLower(ref)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.servers[ref]; ok {
		return ref, nil
	}
	if len(ref) != shortIDLen {
		return "", ErrNotFound
	}
	found := ""
	for id := range m.servers {
		if strings.HasPrefix(id, ref) {
			if found != "" {
				return "", fmt.Errorf("%w: short ID %s matches more than one server; use the full ID", ErrNotFound, ref)
			}
			found = id
		}
	}
	if found == "" {
		return "", ErrNotFound
	}
	return found, nil
}

// FilesDir returns the directory of a server that exists, for file access
// from outside the server (SFTP).
func (m *Manager) FilesDir(id string) (string, error) {
	i, err := m.instance(id)
	if err != nil {
		return "", err
	}
	i.mu.Lock()
	deleted := i.deleted
	i.mu.Unlock()
	if deleted {
		return "", ErrNotFound
	}
	return m.serverDir(id)
}

// CheckFiles reports whether a server's files may be accessed now, and
// written if write is set. Nothing may touch them while an install script
// runs over them or a backup replaces them. With quotas the kernel enforces the disk limit on every
// write; with soft limits (quotas off) writes are refused once a scan found
// the server over its limit, until a new check finds it back under.
func (m *Manager) CheckFiles(ctx context.Context, id string, write bool) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	i.mu.Lock()
	state, deleted, over := i.state, i.deleted, i.overLimit > 0
	i.mu.Unlock()
	switch {
	case deleted:
		return ErrNotFound
	case state == Installing:
		return ErrInstalling
	case state == Restoring:
		return ErrRestoring
	case !write || !over:
		return nil
	}
	srv, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	if m.stillOverLimit(ctx, i, srv) {
		return ErrDiskLimit
	}
	return nil
}

// AllocatedPorts returns every port allocated to a server on the node,
// running or not.
func (m *Manager) AllocatedPorts(ctx context.Context) ([]int, error) {
	allocs, err := m.o.Store.Read.ListAllocations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(allocs))
	for _, a := range allocs {
		out = append(out, int(a.Port))
	}
	return out, nil
}
