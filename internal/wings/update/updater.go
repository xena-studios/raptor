package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"aead.dev/minisign"
	"golang.org/x/mod/semver"
)

// Download limits.
const (
	maxBinary    = 256 << 20
	maxChecksums = 64 << 10
	maxSignature = 4 << 10
)

// Errors for requests that can't be served now.
var (
	ErrBusy        = errors.New("an update is already in progress")
	ErrUnsupported = errors.New("self-update isn't available on this node")
)

// Plan is what an update would do.
type Plan struct {
	Current   string // running version
	Target    string // version to install; empty if there's none
	Channel   string // where Target came from: the channel, "pin", or "requested"
	Available bool   // Target isn't the running version (and, from a channel, is newer)
}

// Updater checks for, downloads, and installs new versions.
type Updater struct {
	Layout    Layout
	Source    Source
	Key       minisign.PublicKey
	StatePath string
	Channel   string // "stable" or "beta"
	Pin       string // a version to stay on, from config.yml
	Current   string // the running version
	// Restart makes Wings exit so systemd starts it again, running the
	// staged version on trial.
	Restart func()
	Log     *slog.Logger

	// Checked before installing; tests replace them.
	executable   func() (string, error)
	underSystemd func() bool

	mu   sync.Mutex
	busy bool
}

// Check reports what would be installed: version if given, else the pinned
// version, else the newest release in the channel.
func (u *Updater) Check(ctx context.Context, version string) (Plan, error) {
	p := Plan{Current: u.Current}
	var tag string
	switch {
	case version != "" || u.Pin != "":
		p.Channel = "requested"
		if version == "" {
			version, p.Channel = u.Pin, "pin"
		}
		t, err := normalize(version)
		if err != nil {
			return p, err
		}
		tag = t
	default:
		p.Channel = u.Channel
		releases, err := u.Source.Releases(ctx)
		if err != nil {
			return p, err
		}
		latest, err := Latest(releases, u.Channel)
		if err != nil {
			return p, err
		}
		tag = latest.Tag
		cur, err := normalize(u.Current)
		if err != nil {
			return p, fmt.Errorf("this is a development build (%s): give a version to install", u.Current)
		}
		if semver.Compare(tag, cur) <= 0 {
			// Never move backwards on our own, e.g. after switching from
			// beta to stable: that waits for the next stable release.
			p.Target = strings.TrimPrefix(cur, "v")
			return p, nil
		}
	}
	p.Target = strings.TrimPrefix(tag, "v")
	p.Available = p.Target != u.Current
	return p, nil
}

// Apply installs what Check picks: it downloads and verifies the new version,
// records it for a trial, and restarts Wings, whose launcher then runs it.
// It returns before the restart.
func (u *Updater) Apply(ctx context.Context, version, actor string) (Plan, error) {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return Plan{}, ErrBusy
	}
	u.busy = true
	u.mu.Unlock()
	restarting := false
	defer func() {
		if !restarting {
			u.mu.Lock()
			u.busy = false
			u.mu.Unlock()
		}
	}()

	if err := u.supported(); err != nil {
		return Plan{}, err
	}
	if st, err := ReadState(u.StatePath); err != nil {
		return Plan{}, err
	} else if st.Status == StatusTrial {
		return Plan{}, fmt.Errorf("%w: %s is on trial", ErrBusy, st.To)
	}
	p, err := u.Check(ctx, version)
	if err != nil || !p.Available {
		return p, err
	}
	log := u.Log.With("from", p.Current, "to", p.Target, "actor", actor)
	log.Info("downloading update")
	if err := u.stage(ctx, p.Target); err != nil {
		return p, err
	}
	st := State{Status: StatusTrial, From: u.Current, To: p.Target, Actor: actor, Started: time.Now()}
	if err := WriteState(u.StatePath, st); err != nil {
		return p, err
	}
	log.Info("update verified; restarting to run it on trial")
	restarting = true
	go func() {
		time.Sleep(time.Second) // let the reply go out
		u.Restart()
	}()
	return p, nil
}

// Status returns the last update.
func (u *Updater) Status() (State, error) { return ReadState(u.StatePath) }

// supported checks that Wings can install versions and restart into them.
func (u *Updater) supported() error {
	exe, err := u.exe()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	if !u.Layout.Holds(exe) {
		return fmt.Errorf("%w: it runs from %s, not from %s where the installer puts it", ErrUnsupported, exe, u.Layout.Dir)
	}
	if !u.systemd() {
		return fmt.Errorf("%w: Wings isn't running under systemd, which starts the new version", ErrUnsupported)
	}
	return nil
}

func (u *Updater) exe() (string, error) {
	if u.executable != nil {
		return u.executable()
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func (u *Updater) systemd() bool {
	if u.underSystemd != nil {
		return u.underSystemd()
	}
	return os.Getenv("INVOCATION_ID") != "" // set by systemd for its services
}

// stage downloads a version into the layout and verifies it: the signed
// checksums, then the binary's SHA-256 against them. Nothing is run or put in
// place before both check out.
func (u *Updater) stage(ctx context.Context, version string) error {
	tag := "v" + version
	var sums, sig bytes.Buffer
	if err := u.Source.Download(ctx, tag, "checksums.txt", maxChecksums, &sums); err != nil {
		return err
	}
	if err := u.Source.Download(ctx, tag, "checksums.txt.minisig", maxSignature, &sig); err != nil {
		return err
	}
	checksums, err := Checksums(u.Key, tag, sums.Bytes(), sig.Bytes())
	if err != nil {
		return err
	}
	asset := "raptor_linux_" + runtime.GOARCH
	want, ok := checksums[asset]
	if !ok {
		return fmt.Errorf("%s has no %s binary", tag, asset)
	}

	if err := u.Layout.Prune(u.Current); err != nil {
		u.Log.Warn("can't remove old versions", "err", err)
	}
	f, err := os.CreateTemp(u.Layout.Dir, tmpPrefix+binPrefix+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // a no-op once renamed
	h := sha256.New()
	err = u.Source.Download(ctx, tag, asset, maxBinary, io.MultiWriter(f, h))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := h.Sum(nil); !bytes.Equal(got, want[:]) {
		return fmt.Errorf("%s doesn't match its signed checksum", asset)
	}
	if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // the raptor CLI is for everyone in the raptor group
		return err
	}
	// It must run here and be the version it claims to be.
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, tmp, "version").Output() //nolint:gosec // verified above
	if err != nil {
		return fmt.Errorf("the new version doesn't run here: %w", err)
	}
	if got, _, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != version {
		return fmt.Errorf("the %s binary says it's version %q", tag, got)
	}
	if err := os.Rename(tmp, u.Layout.Binary(version)); err != nil {
		return err
	}
	return syncDir(u.Layout.Dir)
}
