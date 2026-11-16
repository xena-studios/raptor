// Package addons installs plugins and mods into servers whose egg turns on
// an add-on (docs/EGGS.md#add-ons). Files come only from Modrinth's and
// Hangar's CDNs and uMod, over https, and are kept only if their hash is
// the one asked for (SHA-512 from Modrinth, SHA-256 from Hangar, SHA-1
// from uMod, the only one it publishes), so the Panel
// can't make a node fetch anything else, and a swapped file on the way is
// refused. They're written through the server's file system
// like the file manager's (symlink-safe, the egg's denylist, the disk
// limit), and a replaced version goes to the server's trash.
package addons

import (
	"context"
	"crypto/sha1" //nolint:gosec // uMod publishes only SHA-1s
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
	"github.com/xena-studios/raptor/internal/shared/cffingerprint"
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
	UModHost     = "umod.org"
	// FactorioHost is Factorio's mod portal: its downloads need the
	// account's username and token, which Wings adds from the server's
	// variables (the egg's auth).
	FactorioHost = "mods.factorio.com"
	// CurseForge's CDN: its files list a SHA-1 (and an MD5).
	CurseForgeHost  = "edge.forgecdn.net"
	CurseForgeHost2 = "mediafilez.forgecdn.net"
)

// DefaultHosts are where files may come from.
var DefaultHosts = []string{ModrinthHost, HangarHost, UModHost, FactorioHost, CurseForgeHost, CurseForgeHost2, NuGetHost}

// ErrNoAccount is a download that needs an account the server hasn't set.
var ErrNoAccount = errors.New("the mod portal needs your factorio.com username and token: set them in Startup first")

// Errors.
var (
	ErrNoAddon  = errors.New("this server's egg doesn't have that add-on")
	ErrSource   = errors.New("add-ons are downloaded from Modrinth, Hangar, CurseForge, uMod, Factorio's mod portal, and NuGet only")
	ErrHash     = errors.New("the download doesn't match the file its source lists; nothing was installed")
	ErrName     = errors.New("that isn't an add-on file name")
	ErrTooLarge = fmt.Errorf("add-on files are at most %d MiB", MaxFile>>20)
)

// Servers is what add-ons need of the server manager: the server's egg,
// and its variables (an add-on's folder can depend on one).
type Servers interface {
	Egg(ctx context.Context, id string) (*eggs.Egg, error)
	Variables(ctx context.Context, id string) (map[string]string, error)
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

type fileHashes struct {
	sha512, sha256, sha1 string
	fingerprint          uint32
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
	Sha256   string `json:"sha256,omitempty"`
	Sha1     string `json:"sha1,omitempty"`
	// Fingerprint is CurseForge's (cffingerprint), which it looks files
	// up by.
	Fingerprint uint32 `json:"cf_fingerprint,omitempty"`
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
	if a.DirVariable != "" {
		vars, err := s.Servers.Variables(ctx, id)
		if err != nil {
			return a, err
		}
		a.Dir = a.DirFor(vars)
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
		file.Sha512, file.Sha256, file.Sha1, file.Fingerprint = h.sha512, h.sha256, h.sha1, h.fingerprint
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
	h512, h256, h1 := sha512.New(), sha256.New(), sha1.New() //nolint:gosec // uMod's
	var length cffingerprint.Counter
	if _, err := io.Copy(io.MultiWriter(h512, h256, h1, &length), io.LimitReader(file, MaxFile)); err != nil {
		return fileHashes{}, err
	}
	// CurseForge's fingerprint is seeded with the length, so it's a
	// second pass.
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fileHashes{}, err
	}
	fp := cffingerprint.New(length.N)
	if _, err := io.Copy(fp, io.LimitReader(file, MaxFile)); err != nil {
		return fileHashes{}, err
	}
	sum := fileHashes{hex.EncodeToString(h512.Sum(nil)), hex.EncodeToString(h256.Sum(nil)), hex.EncodeToString(h1.Sum(nil)), fp.Sum32()}
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
	Sha1     string `json:"sha1,omitempty"`
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
	if err := checkNuGetKind(p.Kind, u.Hostname()); err != nil {
		return File{}, err
	}
	want, err := wantHash(u.Hostname(), p)
	if err != nil {
		return File{}, err
	}
	if a.Auth != nil && strings.EqualFold(u.Hostname(), FactorioHost) {
		if u, err = s.withAccount(ctx, id, a.Auth, u); err != nil {
			return File{}, err
		}
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
	download := s.download
	if p.Kind == KindOpenMod {
		download = s.downloadNuGet
	}
	if err := download(ctx, f, tmp, u, want); err != nil {
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
	out := File{Name: p.Filename, Size: fi.Size(), Modified: fi.ModTime().UnixMilli(), Sha512: p.Sha512, Sha256: p.Sha256, Sha1: p.Sha1}
	if p.Kind == KindOpenMod {
		out.Sha512 = "" // the package's, not the assembly's: the listing has it
	}
	return out, nil
}

// withAccount adds the server's account (its variables named by the
// egg's auth) to a download from a source that needs one.
func (s *Service) withAccount(ctx context.Context, id string, auth *eggs.AddonAuth, u *url.URL) (*url.URL, error) {
	vars, err := s.Servers.Variables(ctx, id)
	if err != nil {
		return nil, err
	}
	user, token := strings.TrimSpace(vars[auth.Username]), strings.TrimSpace(vars[auth.Token])
	if user == "" || token == "" {
		return nil, ErrNoAccount
	}
	out := *u
	q := out.Query()
	q.Set("username", user)
	q.Set("token", token)
	out.RawQuery = q.Encode()
	return &out, nil
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
	switch {
	case strings.EqualFold(host, HangarHost):
		hexSum, size, newHash, name = p.Sha256, sha256.Size, sha256.New, "SHA-256"
	case strings.EqualFold(host, UModHost), strings.EqualFold(host, FactorioHost),
		strings.EqualFold(host, CurseForgeHost), strings.EqualFold(host, CurseForgeHost2):
		hexSum, size, newHash, name = p.Sha1, sha1.Size, sha1.New, "SHA-1"
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
		// Not the URL: it can carry an account's token.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("downloading from %s: %w", u.Hostname(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden && u.Query().Has("token") {
		return errors.New("the mod portal refused the username and token: check them in Startup")
	}
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
