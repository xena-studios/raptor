// Package addons installs plugins and mods into servers whose egg turns on
// an add-on (docs/EGGS.md#add-ons). Files come only from Modrinth's CDN,
// over https, and are kept only if their SHA-512 is the one asked for, so
// the Panel can't make a node fetch anything else, and a swapped file on
// the way is refused. They're written through the server's file system
// like the file manager's (symlink-safe, the egg's denylist, the disk
// limit), and a replaced version goes to the server's trash.
package addons

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/files"
)

// Limits.
const (
	// MaxFile is the largest add-on file.
	MaxFile = 256 << 20
	// MaxList is the most files a listing hashes.
	MaxList = 500
	// Timeout bounds one download.
	Timeout = 5 * time.Minute
)

// DefaultHosts are where files may come from.
var DefaultHosts = []string{"cdn.modrinth.com"}

// Errors.
var (
	ErrNoAddon  = errors.New("this server's egg doesn't have that add-on")
	ErrSource   = errors.New("add-ons are downloaded from Modrinth only")
	ErrHash     = errors.New("the download doesn't match the file Modrinth lists; nothing was installed")
	ErrName     = errors.New("that isn't an add-on file name")
	ErrTooLarge = fmt.Errorf("add-on files are at most %d MiB", MaxFile>>20)
)

// Servers is what add-ons need of the server manager: the server's egg.
type Servers interface {
	Egg(ctx context.Context, id string) (*eggs.Egg, error)
}

// Service lists and installs add-on files.
type Service struct {
	Files   *files.Service
	Servers Servers
	// Client downloads files (nil: one that only follows https redirects
	// to Hosts).
	Client *http.Client
	// Hosts are the hosts files may come from (nil: DefaultHosts).
	Hosts     []string
	UserAgent string

	hashes sync.Map // hashKey -> sha512 hex
}

type hashKey struct {
	server, name string
	size, mtime  int64
}

// File is an add-on file in a server.
type File struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified_at"` // unix ms
	Sha512   string `json:"sha512,omitempty"`
	// Disabled: renamed to .disabled, which the software skips.
	Disabled bool `json:"disabled,omitempty"`
}

// Listing is a server's files of one add-on kind.
type Listing struct {
	Kind  string `json:"kind"`
	Dir   string `json:"dir"`
	Files []File `json:"files"`
}

func (s *Service) hosts() []string {
	if s.Hosts != nil {
		return s.Hosts
	}
	return DefaultHosts
}

func (s *Service) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return &http.Client{
		Timeout: Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return s.checkURL(req.URL)
		},
	}
}

func (s *Service) addon(ctx context.Context, id, kind string) (eggs.Addon, error) {
	egg, err := s.Servers.Egg(ctx, id)
	if err != nil {
		return eggs.Addon{}, err
	}
	a, ok := egg.Raptor.Addons[kind]
	if !ok {
		return a, ErrNoAddon
	}
	return a, nil
}

// addonFile reports whether name is a file an add-on folder lists.
func addonFile(name string) (ok, disabled bool) {
	if base, cut := strings.CutSuffix(name, ".disabled"); cut {
		return strings.HasSuffix(base, ".jar"), true
	}
	return strings.HasSuffix(name, ".jar"), false
}

// List lists a server's add-on files of a kind, with their SHA-512s (to
// look them up on Modrinth).
func (s *Service) List(ctx context.Context, id, kind string) (Listing, error) {
	a, err := s.addon(ctx, id, kind)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Kind: kind, Dir: a.Dir, Files: []File{}}
	f, err := s.Files.Open(ctx, id, false)
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close() }()
	infos, err := f.List(a.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, fi := range infos {
		ok, disabled := addonFile(fi.Name())
		if !ok || !fi.Mode().IsRegular() {
			continue
		}
		if len(out.Files) == MaxList {
			break
		}
		file := File{Name: fi.Name(), Size: fi.Size(), Modified: fi.ModTime().UnixMilli(), Disabled: disabled}
		file.Sha512, err = s.hash(ctx, f, id, path.Join(a.Dir, fi.Name()), fi)
		if err != nil {
			return out, err
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

// hash is a file's SHA-512, remembered while its size and time stay the
// same.
func (s *Service) hash(ctx context.Context, f *files.FS, id, name string, fi fs.FileInfo) (string, error) {
	key := hashKey{id, name, fi.Size(), fi.ModTime().UnixNano()}
	if h, ok := s.hashes.Load(key); ok {
		return h.(string), nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := f.Open(name, os.O_RDONLY)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	h := sha512.New()
	if _, err := io.Copy(h, io.LimitReader(file, MaxFile)); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	s.hashes.Store(key, sum)
	return sum, nil
}

// Install is what to install: a file Modrinth lists.
type Install struct {
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Sha512   string `json:"sha512"`
	Filename string `json:"filename"`
	// Replace is the file this one updates, moved to the trash once the
	// new one is in place.
	Replace string `json:"replace,omitempty"`
}

func (s *Service) checkURL(u *url.URL) error {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || !slices.Contains(s.hosts(), strings.ToLower(u.Hostname())) {
		return ErrSource
	}
	return nil
}

// checkName accepts a plain file name ending in .jar.
func checkName(name string) error {
	if name == "" || len(name) > 255 || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) || !strings.HasSuffix(name, ".jar") {
		return ErrName
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return ErrName
		}
	}
	return nil
}

// Install downloads a file into the server's add-on folder. user is who
// asked (for the trash).
func (s *Service) Install(ctx context.Context, id, user string, p Install) (File, error) {
	a, err := s.addon(ctx, id, p.Kind)
	if err != nil {
		return File{}, err
	}
	u, err := url.Parse(p.URL)
	if err != nil || s.checkURL(u) != nil {
		return File{}, ErrSource
	}
	want, err := hex.DecodeString(p.Sha512)
	if err != nil || len(want) != sha512.Size {
		return File{}, errors.New("the file's SHA-512 is needed")
	}
	if err := checkName(p.Filename); err != nil {
		return File{}, err
	}
	if p.Replace != "" {
		if ok, _ := addonFile(p.Replace); !ok || strings.ContainsAny(p.Replace, `/\`) || strings.HasPrefix(p.Replace, ".") {
			return File{}, ErrName
		}
	}
	f, err := s.Files.Open(ctx, id, true)
	if err != nil {
		return File{}, err
	}
	defer func() { _ = f.Close() }()
	if err := f.MkdirAll(a.Dir); err != nil {
		return File{}, err
	}
	tmp := path.Join(a.Dir, ".raptor-"+uuid.NewString()+".part")
	if err := s.download(ctx, f, tmp, u, want); err != nil {
		_ = f.Remove(tmp)
		return File{}, err
	}
	dst := path.Join(a.Dir, p.Filename)
	if err := f.Rename(tmp, dst, true); err != nil {
		_ = f.Remove(tmp)
		return File{}, err
	}
	if p.Replace != "" && p.Replace != p.Filename {
		if err := s.Files.Trash(ctx, id, user, a.Dir, []string{p.Replace}); err != nil {
			return File{}, fmt.Errorf("installed, but the old file is still there: %w", err)
		}
	}
	fi, err := f.Stat(dst)
	if err != nil {
		return File{}, err
	}
	return File{Name: p.Filename, Size: fi.Size(), Modified: fi.ModTime().UnixMilli(), Sha512: p.Sha512}, nil
}

func (s *Service) download(ctx context.Context, f *files.FS, tmp string, u *url.URL, want []byte) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("downloading from Modrinth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading from Modrinth: %s", resp.Status)
	}
	if resp.ContentLength > MaxFile {
		return ErrTooLarge
	}
	out, err := f.Open(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	h := sha512.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, MaxFile+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return err
	case n > MaxFile:
		return ErrTooLarge
	case !slices.Equal(h.Sum(nil), want):
		return ErrHash
	}
	return nil
}
