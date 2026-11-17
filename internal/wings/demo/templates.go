package demo

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/wings/files"
)

// Templates are the warm pool (#449): an installed copy of each of the
// eggs guests pick most, which the Panel has the node build every night. A
// new sandbox of one of them is a copy of its template (an XFS reflink
// where the volume supports it, so instant and taking no space until it
// changes), and starts in seconds instead of minutes. Others install as
// usual.
//
// They live on the server volume (Dir is <volumes>/demo-templates), so a
// reflink can share their blocks with the servers' files.
type Templates struct {
	Dir      string
	UID, GID int
	Log      *slog.Logger
	Now      func() time.Time

	mu sync.Mutex
}

// MaxTemplateAge is how long a template is used: the nightly build
// replaces it, and one that wasn't rebuilt (the egg fell out of the most
// picked) goes.
const MaxTemplateAge = 48 * time.Hour

var keyPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TemplateKey is what a template is for: an egg's code, its image, its
// variables, and whether its EULA was accepted. A sandbox made with all of
// those the same can start from the template.
func TemplateKey(fingerprint, image string, variables map[string]string, eula bool) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%t", fingerprint, image, eula)
	for _, k := range slices.Sorted(maps.Keys(variables)) {
		_, _ = fmt.Fprintf(h, "\x00%s=%s", k, variables[k])
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

type templateMeta struct {
	Egg       string `json:"egg"`
	CreatedAt int64  `json:"created_at"`
}

func (t *Templates) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Has reports whether a fresh template exists for key.
func (t *Templates) Has(key string) bool {
	if t == nil || !keyPattern.MatchString(key) {
		return false
	}
	m, err := t.meta(key)
	return err == nil && t.now().Sub(time.Unix(m.CreatedAt, 0)) < MaxTemplateAge
}

func (t *Templates) meta(key string) (templateMeta, error) {
	var m templateMeta
	b, err := os.ReadFile(filepath.Join(t.Dir, key+".json")) //nolint:gosec // key is checked against keyPattern by every caller
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// Capture copies a template build's installed files into the template for
// key, replacing any older one.
func (t *Templates) Capture(ctx context.Context, key, egg, src string) error {
	if !keyPattern.MatchString(key) {
		return errors.New("bad template key")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(t.Dir, "."+key+"-"+randomSuffix())
	if err := os.Mkdir(tmp, 0o750); err != nil {
		return err
	}
	if err := copyTree(ctx, src, tmp, t.UID, t.GID); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	dst := filepath.Join(t.Dir, key)
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	b, _ := json.Marshal(templateMeta{Egg: egg, CreatedAt: t.now().Unix()})
	return os.WriteFile(filepath.Join(t.Dir, key+".json"), b, 0o600)
}

// CopyTo fills a new server's directory from the template for key.
func (t *Templates) CopyTo(ctx context.Context, key, dir string) error {
	if !t.Has(key) {
		return errors.New("no such template")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return copyTree(ctx, filepath.Join(t.Dir, key), dir, t.UID, t.GID)
}

// List returns the fresh templates' keys and eggs.
func (t *Templates) List() map[string]string {
	out := map[string]string{}
	if t == nil {
		return out
	}
	ents, _ := os.ReadDir(t.Dir)
	for _, e := range ents {
		key, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !t.Has(key) {
			continue
		}
		if m, err := t.meta(key); err == nil {
			out[key] = m.Egg
		}
	}
	return out
}

// Prune deletes stale templates, and the oldest beyond keep, and leftovers
// of captures that didn't finish.
func (t *Templates) Prune(keep int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ents, err := os.ReadDir(t.Dir)
	if err != nil {
		return
	}
	type tpl struct {
		key string
		at  int64
	}
	var fresh []tpl
	for _, e := range ents {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "."):
			if info, err := e.Info(); err == nil && t.now().Sub(info.ModTime()) > time.Hour {
				_ = os.RemoveAll(filepath.Join(t.Dir, name))
			}
		case strings.HasSuffix(name, ".json"):
			key := strings.TrimSuffix(name, ".json")
			m, err := t.meta(key)
			if err != nil || t.now().Sub(time.Unix(m.CreatedAt, 0)) >= MaxTemplateAge {
				t.removeLocked(key)
				continue
			}
			fresh = append(fresh, tpl{key, m.CreatedAt})
		case e.IsDir() && keyPattern.MatchString(name):
			if _, err := os.Stat(filepath.Join(t.Dir, name+".json")); err != nil {
				_ = os.RemoveAll(filepath.Join(t.Dir, name))
			}
		}
	}
	sort.Slice(fresh, func(a, b int) bool { return fresh[a].at > fresh[b].at })
	for i, f := range fresh {
		if i >= keep {
			t.removeLocked(f.key)
		}
	}
}

func (t *Templates) removeLocked(key string) {
	_ = os.Remove(filepath.Join(t.Dir, key+".json"))
	_ = os.RemoveAll(filepath.Join(t.Dir, key))
	if t.Log != nil {
		t.Log.Info("demo: template removed", "template", key)
	}
}

// copyTree copies src into dst (which exists), as the raptor user. Files
// are reflinked where the filesystem supports it (files.FS.CopyTree).
func copyTree(ctx context.Context, src, dst string, uid, gid int) error {
	in, err := os.OpenRoot(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := files.Open(dst, uid, gid, nil)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = out.CopyTree(ctx, in, nil)
	return err
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
