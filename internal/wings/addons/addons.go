// Package addons installs plugins and mods into servers whose egg turns on
// an add-on (docs/EGGS.md#add-ons). Files come only from Modrinth's and
// Hangar's CDNs, over https, and are kept only if their hash is the one
// asked for (SHA-512 from Modrinth, SHA-256 from Hangar), so the Panel
// can't make a node fetch anything else, and a swapped file on the way is
// refused. They're written through the server's file system
// like the file manager's (symlink-safe, the egg's denylist, the disk
// limit), and a replaced version goes to the server's trash.
package addons

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
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

// Where files may come from, and the hash each lists.
const (
	ModrinthHost = "cdn.modrinth.com"
	HangarHost   = "hangarcdn.papermc.io"
)

// DefaultHosts are where files may come from.
var DefaultHosts = []string{ModrinthHost, HangarHost}

// Errors.
var (
	ErrNoAddon  = errors.New("this server's egg doesn't have that add-on")
	ErrSource   = errors.New("add-ons are downloaded from Modrinth and Hangar only")
	ErrHash     = errors.New("the download doesn't match the file its source lists; nothing was installed")
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

	hashes sync.Map // hashKey -> fileHashes
}

type fileHashes struct{ sha512, sha256 string }

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
	Sha256   string `json:"sha256,omitempty"`
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

// addonFile reports whether name is a file of a kind (by its extension:
// .jar, or .zip for datapacks) that its folder lists.
func addonFile(kind, name string) (ok, disabled bool) {
	ext := eggs.AddonExt[kind]
	if base, cut := strings.CutSuffix(name, ".disabled"); cut {
		return ext != "" && strings.HasSuffix(base, ext), true
	}
	return ext != "" && strings.HasSuffix(name, ext), false
}

// List lists a server's add-on files of a kind, with their SHA-512s and
// SHA-256s (to look them up on Modrinth and Hangar).
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
		ok, disabled := addonFile(kind, fi.Name())
		if !ok || !fi.Mode().IsRegular() {
			continue
		}
		if len(out.Files) == MaxList {
			break
		}
		file := File{Name: fi.Name(), Size: fi.Size(), Modified: fi.ModTime().UnixMilli(), Disabled: disabled}
		h, err := s.hash(ctx, f, id, path.Join(a.Dir, fi.Name()), fi)
		if err != nil {
			return out, err
		}
		file.Sha512, file.Sha256 = h.sha512, h.sha256
		out.Files = append(out.Files, file)
	}
	return out, nil
}

// hash is a file's SHA-512 and SHA-256, remembered while its size and
// time stay the same.
func (s *Service) hash(ctx context.Context, f *files.FS, id, name string, fi fs.FileInfo) (fileHashes, error) {
	key := hashKey{id, name, fi.Size(), fi.ModTime().UnixNano()}
	if h, ok := s.hashes.Load(key); ok {
		return h.(fileHashes), nil
	}
	if err := ctx.Err(); err != nil {
		return fileHashes{}, err
	}
	file, err := f.Open(name, os.O_RDONLY)
	if err != nil {
		return fileHashes{}, err
	}
	defer func() { _ = file.Close() }()
	h512, h256 := sha512.New(), sha256.New()
	if _, err := io.Copy(io.MultiWriter(h512, h256), io.LimitReader(file, MaxFile)); err != nil {
		return fileHashes{}, err
	}
	sum := fileHashes{hex.EncodeToString(h512.Sum(nil)), hex.EncodeToString(h256.Sum(nil))}
	s.hashes.Store(key, sum)
	return sum, nil
}

// Install is what to install: a file Modrinth or Hangar lists, with the
// hash its source gives (SHA-512 for Modrinth, SHA-256 for Hangar).
type Install struct {
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Sha512   string `json:"sha512,omitempty"`
	Sha256   string `json:"sha256,omitempty"`
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

// checkName accepts a plain file name with a kind's extension.
func checkName(kind, name string) error {
	ext := eggs.AddonExt[kind]
	if name == "" || len(name) > 255 || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) || ext == "" || !strings.HasSuffix(name, ext) {
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
	want, err := wantHash(u.Hostname(), p)
	if err != nil {
		return File{}, err
	}
	if err := checkName(p.Kind, p.Filename); err != nil {
		return File{}, err
	}
	if p.Replace != "" {
		if ok, _ := addonFile(p.Kind, p.Replace); !ok || strings.ContainsAny(p.Replace, `/\`) || strings.HasPrefix(p.Replace, ".") {
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
	return File{Name: p.Filename, Size: fi.Size(), Modified: fi.ModTime().UnixMilli(), Sha512: p.Sha512, Sha256: p.Sha256}, nil
}

// expected is the hash a download must have.
type expected struct {
	new func() hash.Hash
	sum []byte
}

// wantHash is the hash a file from a host must have: the one its source
// lists. A file from Modrinth needs its SHA-512 and one from Hangar its
// SHA-256, so the weaker hash is never enough for Modrinth's files.
func wantHash(host string, p Install) (expected, error) {
	hexSum, size, newHash, name := p.Sha512, sha512.Size, sha512.New, "SHA-512"
	if strings.EqualFold(host, HangarHost) {
		hexSum, size, newHash, name = p.Sha256, sha256.Size, sha256.New, "SHA-256"
	}
	sum, err := hex.DecodeString(hexSum)
	if err != nil || len(sum) != size {
		return expected{}, fmt.Errorf("the file's %s is needed", name)
	}
	return expected{newHash, sum}, nil
}

func (s *Service) download(ctx context.Context, f *files.FS, tmp string, u *url.URL, want expected) error {
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
		return fmt.Errorf("downloading from %s: %w", u.Hostname(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading from %s: %s", u.Hostname(), resp.Status)
	}
	if resp.ContentLength > MaxFile {
		return ErrTooLarge
	}
	out, err := f.Open(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	h := want.new()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, MaxFile+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return err
	case n > MaxFile:
		return ErrTooLarge
	case !slices.Equal(h.Sum(nil), want.sum):
		return ErrHash
	}
	return nil
}
