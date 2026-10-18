package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
)

// Importing a server's files from another host over SFTP
// (docs/WINGS.md#files-and-sftp): an old panel, a game host, a shared
// host. The node connects to the other host itself, only to public
// addresses (as downloads do), checks its host key against the one the
// owner confirmed, and copies its files in. Nothing already there is
// replaced: files with the same name are left as they are and counted,
// so an import can't destroy anything. The password or key is kept in
// memory only, for the one import; if Wings restarts meanwhile, the import
// fails and is started again.

// JobImport is the job that imports files.
const JobImport = "files.import"

// EventImported is recorded when an import finishes or fails.
const EventImported = "server.files.imported"

// Limits of an import.
const (
	maxImportEntries = 1_000_000
	// importIdle: an import that copies nothing for this long fails.
	importIdle = 2 * time.Minute
)

// ImportSource is where files are imported from.
type ImportSource struct {
	Host string `json:"host"`
	Port int    `json:"port,omitempty"` // 22 if 0
	User string `json:"user"`
	// One of these signs in.
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"` // PEM, unencrypted
	// HostKey is the host's key the owner confirmed, in authorized_keys
	// form (files.import.hostkey reads it).
	HostKey string `json:"host_key"`
	// Path is the folder on the host to copy ("" or "/" for where the
	// login starts).
	Path string `json:"path,omitempty"`
}

// ImportJob is the payload of an import: everything but the secret.
type ImportJob struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	User    string `json:"user"`
	HostKey string `json:"host_key"`
	Path    string `json:"path"`
	Dir     string `json:"dir"` // where in the server they go
	// Secret names the password or key, kept in memory.
	Secret string `json:"secret"`
	UserID string `json:"user_id,omitempty"`
}

// ImportStatus is how an import is going.
type ImportStatus struct {
	JobID   string `json:"job_id"`
	Host    string `json:"host"`
	Status  string `json:"status"`
	Files   int64  `json:"files"`
	Bytes   int64  `json:"bytes"`
	Skipped int64  `json:"skipped"` // already there, denied, or not a file or folder
	Current string `json:"current,omitempty"`
	Error   string `json:"error,omitempty"`
}

type importProgress struct {
	files, bytes, skipped atomic.Int64
	current               atomic.Pointer[string]
}

type importSecret struct {
	password, key string
}

type imports struct {
	mu       sync.Mutex
	secrets  map[string]importSecret
	progress map[string]*importProgress
}

func (im *imports) keep(s importSecret) string {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.secrets == nil {
		im.secrets = map[string]importSecret{}
	}
	id := uuid.NewString()
	im.secrets[id] = s
	return id
}

func (im *imports) take(id string) (importSecret, bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	s, ok := im.secrets[id]
	delete(im.secrets, id)
	return s, ok
}

func (im *imports) start(jobID string) *importProgress {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.progress == nil {
		im.progress = map[string]*importProgress{}
	}
	p := &importProgress{}
	im.progress[jobID] = p
	return p
}

func (im *imports) get(jobID string) *importProgress {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.progress[jobID]
}

func (im *imports) done(jobID string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	delete(im.progress, jobID)
}

// dialPublic connects only to the addresses allowed accepts, checked after
// the name is resolved.
func dialPublic(ctx context.Context, allowed func(netip.Addr) bool, addr string) (net.Conn, error) {
	d := net.Dialer{
		Timeout: 15 * time.Second,
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
	conn, err := d.DialContext(ctx, "tcp", addr)
	if errors.Is(err, ErrNotPublic) {
		return nil, ErrNotPublic
	}
	return conn, err
}

func importAddr(host string, port int) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/ \t") || len(host) > 253 {
		return "", errors.New("give the host's name or address")
	}
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return "", errors.New("the port must be between 1 and 65535")
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port)), nil
}

// errGotHostKey ends a handshake once the host key is seen.
var errGotHostKey = errors.New("got the host key")

// ImportHostKey reads an SFTP host's key, for the owner to confirm before
// importing from it.
func (s *Service) ImportHostKey(ctx context.Context, host string, port int) (map[string]string, error) {
	addr, err := importAddr(host, port)
	if err != nil {
		return nil, err
	}
	conn, err := dialPublic(ctx, s.allowed(), addr)
	if err != nil {
		return nil, explainImport(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var got ssh.PublicKey
	_, _, _, err = ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User: "raptor",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errGotHostKey
		},
	})
	if got == nil {
		return nil, explainImport(err)
	}
	return map[string]string{
		"key":         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(got))),
		"fingerprint": ssh.FingerprintSHA256(got),
	}, nil
}

// Import queues an import of src into dir in the server, and returns the
// job's ID.
func (s *Service) Import(ctx context.Context, id, user string, src ImportSource, dir string) (string, error) {
	addr, err := importAddr(src.Host, src.Port)
	if err != nil {
		return "", err
	}
	host, portS, _ := net.SplitHostPort(addr)
	if ip, err := netip.ParseAddr(host); err == nil && !s.allowed()(ip) {
		return "", ErrNotPublic
	}
	port, _ := strconv.Atoi(portS)
	switch {
	case strings.TrimSpace(src.User) == "":
		return "", errors.New("give the username to sign in with")
	case src.Password == "" && src.PrivateKey == "":
		return "", errors.New("give a password or a private key")
	}
	if src.PrivateKey != "" {
		if _, err := ssh.ParsePrivateKey([]byte(src.PrivateKey)); err != nil {
			return "", errors.New("the private key couldn't be read: give an unencrypted key in PEM or OpenSSH form")
		}
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(src.HostKey)); err != nil {
		return "", errors.New("the host key is missing: check the host's key first")
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
		return "", &fs.PathError{Op: "import", Path: dir, Err: syscall.ENOTDIR}
	}
	secret := s.importing.keep(importSecret{password: src.Password, key: src.PrivateKey})
	jobID, err := s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobImport, ServerID: id, Payload: ImportJob{
		Host: host, Port: port, User: strings.TrimSpace(src.User), HostKey: src.HostKey,
		Path: src.Path, Dir: dir, Secret: secret, UserID: user,
	}})
	if err != nil {
		s.importing.take(secret)
	}
	return jobID, err
}

// ImportStatus says how an import is going.
func (s *Service) ImportStatus(ctx context.Context, id, jobID string) (ImportStatus, error) {
	j, err := s.o.Jobs.Get(ctx, jobID)
	if err != nil || j.ServerID != id || j.Type != JobImport {
		return ImportStatus{}, ErrTransferNotFound
	}
	var p ImportJob
	if err := j.Decode(&p); err != nil {
		return ImportStatus{}, err
	}
	st := ImportStatus{JobID: j.ID, Host: p.Host, Status: j.Status, Error: j.Error}
	if pr := s.importing.get(j.ID); pr != nil {
		st.Files, st.Bytes, st.Skipped = pr.files.Load(), pr.bytes.Load(), pr.skipped.Load()
		if c := pr.current.Load(); c != nil {
			st.Current = *c
		}
	} else if len(j.Result) > 0 {
		var r ImportStatus
		if err := json.Unmarshal(j.Result, &r); err == nil {
			st.Files, st.Bytes, st.Skipped = r.Files, r.Bytes, r.Skipped
		}
	}
	return st, nil
}

// CancelImport stops an import; what was copied stays.
func (s *Service) CancelImport(ctx context.Context, id, jobID string) error {
	if _, err := s.ImportStatus(ctx, id, jobID); err != nil {
		return err
	}
	return s.o.Jobs.Cancel(ctx, jobID)
}

func (s *Service) importJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p ImportJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	pr := s.importing.start(j.ID)
	defer s.importing.done(j.ID)
	_, _ = fmt.Fprintf(log, "importing %s@%s:%d %s into /%s\n", p.User, p.Host, p.Port, p.Path, p.Dir)
	err := s.runImport(ctx, j, p, pr)
	res := ImportStatus{Files: pr.files.Load(), Bytes: pr.bytes.Load(), Skipped: pr.skipped.Load()}
	data := map[string]any{"job_id": j.ID, "user_id": p.UserID, "host": p.Host, "files": res.Files, "bytes": res.Bytes, "skipped": res.Skipped}
	if err != nil {
		data["error"] = err.Error()
		_, _ = fmt.Fprintf(log, "failed after %d file(s): %v\n", res.Files, err)
	} else {
		_, _ = fmt.Fprintf(log, "done: %d file(s), %d bytes, %d left out\n", res.Files, res.Bytes, res.Skipped)
	}
	if ctx.Err() == nil && s.o.Events != nil {
		if _, aerr := s.o.Events.Append(context.WithoutCancel(ctx), events.Event{Type: EventImported, ServerID: j.ServerID, Data: data}); aerr != nil {
			s.o.Log.Error("recording event failed", "server", j.ServerID, "event", EventImported, "err", aerr)
		}
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) runImport(ctx context.Context, j jobs.Job, p ImportJob, pr *importProgress) error {
	secret, ok := s.importing.take(p.Secret)
	if !ok {
		return errors.New("the node restarted during the import, and doesn't keep the password: start it again")
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(p.HostKey))
	if err != nil {
		return err
	}
	var auth []ssh.AuthMethod
	if secret.key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(secret.key))
		if err != nil {
			return err
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if secret.password != "" {
		pw := secret.password
		auth = append(auth, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			out := make([]string, len(qs))
			for i := range qs {
				out[i] = pw
			}
			return out, nil
		}))
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	conn, err := dialPublic(ctx, s.allowed(), addr)
	if err != nil {
		return explainImport(err)
	}
	defer func() { _ = conn.Close() }()
	// Closing the connection is how a stall, or a cancel, interrupts a
	// read in the middle.
	idle := time.AfterFunc(importIdle, func() {
		cancel(fmt.Errorf("nothing arrived for %s", importIdle))
		_ = conn.Close()
	})
	defer idle.Stop()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User: p.User, Auth: auth, HostKeyCallback: ssh.FixedHostKey(hostKey), Timeout: 30 * time.Second,
	})
	if err != nil {
		return explainImport(err)
	}
	_ = conn.SetDeadline(time.Time{})
	client := ssh.NewClient(sc, chans, reqs)
	defer func() { _ = client.Close() }()
	remote, err := sftp.NewClient(client, sftp.UseConcurrentReads(true))
	if err != nil {
		return explainImport(err)
	}
	defer func() { _ = remote.Close() }()

	root := p.Path
	if root == "" {
		if root, err = remote.Getwd(); err != nil {
			root = "."
		}
	}
	if fi, err := remote.Stat(root); err != nil {
		return explainImport(err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%s on %s isn't a folder", root, p.Host)
	}

	f, err := s.open(ctx, j.ServerID, true)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	space, err := s.o.Servers.SpaceLeft(ctx, j.ServerID)
	if err != nil {
		return err
	}

	failed := func(err error) error {
		if c := context.Cause(ctx); c != nil && ctx.Err() != nil {
			return c
		}
		return explainImport(err)
	}
	walker := remote.Walk(root)
	entries := 0
	for walker.Step() {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if err := walker.Err(); err != nil {
			if walker.Path() == root {
				return failed(err)
			}
			pr.skipped.Add(1)
			continue
		}
		entries++
		if entries > maxImportEntries {
			return fmt.Errorf("stopped after %d files and folders", maxImportEntries)
		}
		rel := strings.TrimPrefix(path.Clean(strings.TrimPrefix(walker.Path(), root)), "/")
		if rel == "" || rel == "." {
			continue
		}
		dest := Join(p.Dir, rel)
		fi := walker.Stat()
		switch {
		case fi.IsDir():
			if f.Denied(dest, true) {
				pr.skipped.Add(1)
				walker.SkipDir()
				continue
			}
			if err := f.MkdirAll(dest); err != nil {
				pr.skipped.Add(1)
				walker.SkipDir()
			}
		case fi.Mode().IsRegular():
			name := dest
			pr.current.Store(&name)
			if IsStaging(dest) || f.Denied(dest, false) {
				pr.skipped.Add(1)
				continue
			}
			if _, err := f.Lstat(dest); err == nil {
				pr.skipped.Add(1)
				continue
			}
			if space >= 0 && fi.Size() > space {
				return ErrNoSpace
			}
			n, err := s.importFile(remote, f, walker.Path(), dest, idle, pr)
			if err != nil {
				if errors.Is(err, ErrNoSpace) || ctx.Err() != nil {
					return failed(err)
				}
				// One unreadable file doesn't stop the rest.
				s.o.Log.Warn("import: a file couldn't be copied", "server", j.ServerID, "file", walker.Path(), "err", err)
				pr.skipped.Add(1)
				continue
			}
			if space >= 0 {
				space -= n
			}
			pr.files.Add(1)
		default:
			// Links and special files: a link could point anywhere on the
			// other host's machine, or this one's.
			pr.skipped.Add(1)
		}
	}
	return nil
}

func (s *Service) importFile(remote *sftp.Client, f *FS, from, to string, idle *time.Timer, pr *importProgress) (int64, error) {
	src, err := remote.Open(from)
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	staging := stagingPrefix + "import-" + uuid.NewString()
	if dir := path.Dir(to); dir != "." {
		staging = dir + "/" + staging
	}
	out, err := f.Open(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return 0, err
	}
	kept := false
	defer func() {
		if !kept {
			_ = f.Remove(staging)
		}
	}()
	n, err := io.Copy(out, &countingReader{r: src, bytes: &pr.bytes, idle: idle})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if errors.Is(err, syscall.EDQUOT) || errors.Is(err, syscall.ENOSPC) {
			return n, ErrNoSpace
		}
		return n, err
	}
	if err := f.Rename(staging, to, false); err != nil {
		return n, err
	}
	kept = true
	return n, nil
}

// countingReader adds what's read to bytes, and puts off the idle timeout.
type countingReader struct {
	r     io.Reader
	bytes *atomic.Int64
	idle  *time.Timer
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	if n > 0 {
		c.bytes.Add(int64(n))
		c.idle.Reset(importIdle)
	}
	return n, err
}

// explainImport says what went wrong connecting to the other host, in
// words for people.
func explainImport(err error) error {
	if err == nil || errors.Is(err, ErrNotPublic) || errors.Is(err, ErrNoSpace) {
		return err
	}
	s := strings.ToLower(err.Error())
	has := func(subs ...string) bool {
		for _, x := range subs {
			if strings.Contains(s, x) {
				return true
			}
		}
		return false
	}
	switch {
	case has("host key mismatch", "ssh: host key"):
		return errors.New("the host's key isn't the one you confirmed; check the host again")
	case has("unable to authenticate", "permission denied", "no supported methods"):
		return errors.New("the username, password, or key was refused")
	case has("no such host", "server misbehaving"):
		return errors.New("the host's name doesn't resolve; check it's spelled right")
	case has("connection refused"):
		return errors.New("the host refused the connection; check the address and port")
	case has("i/o timeout", "deadline exceeded", "timed out", "no route to host", "network is unreachable"):
		return errors.New("the host didn't answer in time; check the address, and that it lets this node in")
	case has("file does not exist", "no such file"):
		return errors.New("that folder doesn't exist on the host")
	case has("subsystem request failed", "unexpected packet"):
		return errors.New("the host doesn't offer SFTP on that port")
	}
	return fmt.Errorf("the import failed: %w", err)
}
