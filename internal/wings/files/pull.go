package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
)

// Downloading a URL straight into a server's files (docs/WINGS.md#files-and-sftp),
// so a big file doesn't go through the owner's computer. The node only
// connects to public addresses: whoever may write a server's files can't
// use it to reach the owner's network, the machine itself, or a cloud
// provider's metadata service, whatever the URL's name resolves to or
// redirects to.

// JobPull is the job that downloads a URL.
const JobPull = "files.pull"

// EventPulled is recorded when a download finishes or fails.
const EventPulled = "server.files.pulled"

// Limits of a download.
const (
	// MaxPull is the largest file downloaded.
	MaxPull = 50 << 30
	// maxURL is the longest URL taken.
	maxURL = 2048
	// pullIdle: a download that receives nothing for this long fails.
	pullIdle = 2 * time.Minute
	// pullRedirects is how many redirects are followed.
	pullRedirects = 5
)

// Download errors.
var (
	ErrNotPublic = errors.New("that address isn't on the public internet, so the node won't download from it")
	ErrBadURL    = errors.New("give an http:// or https:// link")
)

// PullJob is the payload of a download.
type PullJob struct {
	URL    string `json:"url"`
	Path   string `json:"path"` // the file to make, from the server's directory
	UserID string `json:"user_id,omitempty"`
}

// PullStatus is how a download is going.
type PullStatus struct {
	JobID    string `json:"job_id"`
	URL      string `json:"url"`
	Path     string `json:"path"`
	Status   string `json:"status"` // the job's: queued, running, succeeded, failed, cancelled
	Received int64  `json:"received"`
	Size     int64  `json:"size"` // 0 if the server didn't say
	Error    string `json:"error,omitempty"`
}

type pullProgress struct {
	received, size atomic.Int64
}

// pulls tracks the downloads running now, by job ID.
type pulls struct {
	mu sync.Mutex
	m  map[string]*pullProgress
}

func (p *pulls) start(id string) *pullProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]*pullProgress{}
	}
	pr := &pullProgress{}
	p.m[id] = pr
	return pr
}

func (p *pulls) get(id string) *pullProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m[id]
}

func (p *pulls) done(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, id)
}

// Public reports whether an address is on the public internet: not the
// machine itself, a private or shared network, link-local (where cloud
// metadata services are), multicast, or reserved.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, p := range notPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var notPublic = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",       // this network
		"100.64.0.0/10",   // carrier-grade NAT, Tailscale
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"240.0.0.0/4",     // reserved, and broadcast
		"64:ff9b::/96",    // NAT64: an IPv4 address in disguise
		"64:ff9b:1::/48",  // local NAT64
		"2002::/16",       // 6to4: likewise
		"2001::/32",       // Teredo
		"2001:db8::/32",   // documentation
		"fec0::/10",       // site-local
		"100::/64",        // discard
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// pullClient is an HTTP client that only connects to the addresses
// allowed accepts. Each connection is checked as it's made, after its name
// is resolved, so neither DNS nor a redirect can lead it elsewhere; no
// proxy from the environment is used.
func pullClient(allowed func(netip.Addr) bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !allowed(ip) {
				return ErrNotPublic
			}
			return nil
		},
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSHandshakeTimeout:    30 * time.Second,
			ResponseHeaderTimeout:  time.Minute,
			MaxResponseHeaderBytes: 64 << 10,
			// The file as it's served: Go would otherwise ask for gzip and
			// unpack it, and the size wouldn't be the one announced.
			DisableCompression: true,
			ForceAttemptHTTP2:  true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= pullRedirects {
				return fmt.Errorf("stopped after %d redirects", pullRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return ErrBadURL
			}
			return nil
		},
	}
}

// parsePullURL checks a URL to download.
func parsePullURL(raw string, allowed func(netip.Addr) bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > maxURL {
		return nil, fmt.Errorf("the link is too long (at most %d characters)", maxURL)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, ErrBadURL
	}
	if u.User != nil {
		return nil, errors.New("links with a username or password in them aren't taken: they'd be kept in the node's records")
	}
	// An address typed in is refused now; a name is checked when it's
	// resolved.
	if ip, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); err == nil && !allowed(ip) {
		return nil, ErrNotPublic
	}
	return u, nil
}

// pullName is a file name from a URL's path ("download" if it has none).
func pullName(u *url.URL) string {
	name := path.Base(u.Path)
	if name == "/" || name == "." || name == "" {
		return "download"
	}
	return name
}

// cleanName refuses names that aren't one file's.
func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "", name == ".", name == "..", strings.ContainsAny(name, "/\\\x00"), len(name) > 255:
		return "", errors.New(`a file name can't be empty, contain a slash, or be "." or ".."`)
	case IsStaging(name):
		return "", errors.New("that name is kept for the node's own use")
	}
	return name, nil
}

// Pull queues a download of rawURL into dir as name (from the URL if
// empty), and returns the job's ID. It refuses now what's sure to fail:
// a bad link, a private address, a name that's taken or denied.
func (s *Service) Pull(ctx context.Context, id, user, rawURL, dir, name string) (string, error) {
	u, err := parsePullURL(rawURL, s.allowed())
	if err != nil {
		return "", err
	}
	if name == "" {
		name = pullName(u)
	}
	if name, err = cleanName(name); err != nil {
		return "", err
	}
	f, err := s.open(ctx, id, true)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	dir = Rel(dir)
	if fi, err := f.Stat(dir); err != nil {
		return "", err
	} else if !fi.IsDir() {
		return "", &fs.PathError{Op: "download", Path: dir, Err: syscall.ENOTDIR}
	}
	target := Join(dir, name)
	if err := f.check(target, false); err != nil {
		return "", err
	}
	if _, err := f.Lstat(target); err == nil {
		return "", &fs.PathError{Op: "download", Path: target, Err: fs.ErrExist}
	}
	return s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobPull, ServerID: id, Payload: PullJob{URL: u.String(), Path: target, UserID: user}})
}

// PullStatus says how a download is going.
func (s *Service) PullStatus(ctx context.Context, id, jobID string) (PullStatus, error) {
	j, err := s.o.Jobs.Get(ctx, jobID)
	if err != nil || j.ServerID != id || j.Type != JobPull {
		return PullStatus{}, ErrTransferNotFound
	}
	var p PullJob
	if err := j.Decode(&p); err != nil {
		return PullStatus{}, err
	}
	st := PullStatus{JobID: j.ID, URL: p.URL, Path: p.Path, Status: j.Status, Error: j.Error}
	if pr := s.pulling.get(j.ID); pr != nil {
		st.Received, st.Size = pr.received.Load(), pr.size.Load()
	}
	return st, nil
}

// CancelPull stops a download; what it had fetched is deleted.
func (s *Service) CancelPull(ctx context.Context, id, jobID string) error {
	if _, err := s.PullStatus(ctx, id, jobID); err != nil {
		return err
	}
	return s.o.Jobs.Cancel(ctx, jobID)
}

func pullStaging(jobID string) string { return stagingPrefix + "pull-" + jobID }

func (s *Service) pullJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p PullJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	pr := s.pulling.start(j.ID)
	defer s.pulling.done(j.ID)
	_, _ = fmt.Fprintf(log, "downloading %s to /%s\n", p.URL, p.Path)
	n, err := s.pull(ctx, j, p, pr)
	data := map[string]any{"job_id": j.ID, "user_id": p.UserID, "path": p.Path, "bytes": n}
	if err != nil {
		data["error"] = err.Error()
		_, _ = fmt.Fprintf(log, "failed after %d bytes: %v\n", n, err)
	} else {
		_, _ = fmt.Fprintf(log, "done: %d bytes\n", n)
	}
	if ctx.Err() == nil && s.o.Events != nil {
		if _, aerr := s.o.Events.Append(context.WithoutCancel(ctx), events.Event{Type: EventPulled, ServerID: j.ServerID, Data: data}); aerr != nil {
			s.o.Log.Error("recording event failed", "server", j.ServerID, "event", EventPulled, "err", aerr)
		}
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": p.Path, "bytes": n}, nil
}

func (s *Service) pull(ctx context.Context, j jobs.Job, p PullJob, pr *pullProgress) (int64, error) {
	u, err := parsePullURL(p.URL, s.allowed())
	if err != nil {
		return 0, err
	}
	f, err := s.open(ctx, j.ServerID, true)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	limit := int64(MaxPull)
	space, err := s.o.Servers.SpaceLeft(ctx, j.ServerID)
	if err != nil {
		return 0, err
	}
	if space >= 0 && space < limit {
		limit = space
	}

	// A download that stops receiving is given up on.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	idle := time.AfterFunc(pullIdle, func() { cancel(fmt.Errorf("nothing arrived for %s", pullIdle)) })
	defer idle.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Raptor-Wings (+https://raptorpanel.net)")
	res, err := s.pullClient().Do(req)
	if err != nil {
		if c := context.Cause(ctx); c != nil && ctx.Err() != nil {
			return 0, c
		}
		for _, e := range []error{ErrNotPublic, ErrBadURL} {
			if errors.Is(err, e) {
				return 0, e
			}
		}
		return 0, fmt.Errorf("couldn't reach %s: %w", u.Hostname(), err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s answered %s", u.Hostname(), res.Status)
	}
	size := max(res.ContentLength, 0)
	pr.size.Store(size)
	switch {
	case size > MaxPull:
		return 0, fmt.Errorf("%w: the file is %d bytes, and downloads are at most %d GiB", ErrTooLarge, size, MaxPull>>30)
	case size > limit:
		return 0, fmt.Errorf("%w: the file is %d bytes and the server has %d left", ErrNoSpace, size, limit)
	}

	staging := pullStaging(j.ID)
	out, err := f.Open(staging, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return 0, err
	}
	kept := false
	defer func() {
		if !kept {
			_ = f.Remove(staging)
		}
	}()
	n, err := io.Copy(out, &progressReader{r: io.LimitReader(res.Body, limit+1), pr: pr, idle: idle})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil && context.Cause(ctx) != nil && ctx.Err() != nil:
		return n, context.Cause(ctx)
	case err != nil:
		return n, fmt.Errorf("the download broke off: %w", err)
	case n > limit && limit == MaxPull:
		return n, fmt.Errorf("%w: downloads are at most %d GiB", ErrTooLarge, MaxPull>>30)
	case n > limit:
		return n, ErrNoSpace
	case size > 0 && n != size:
		return n, fmt.Errorf("the download broke off after %d of %d bytes", n, size)
	}
	if err := f.Rename(staging, p.Path, false); err != nil {
		return n, err
	}
	kept = true
	return n, nil
}

func (s *Service) allowed() func(netip.Addr) bool {
	if s.o.PullAllowed != nil {
		return s.o.PullAllowed
	}
	return Public
}

func (s *Service) pullClient() *http.Client { return pullClient(s.allowed()) }

// progressReader counts what's read, and puts off the idle timeout while
// bytes arrive.
type progressReader struct {
	r    io.Reader
	pr   *pullProgress
	idle *time.Timer
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.pr.received.Add(int64(n))
		p.idle.Reset(pullIdle)
	}
	return n, err
}
