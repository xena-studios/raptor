package update

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aead.dev/minisign"
)

// fakeSource serves releases from memory.
type fakeSource struct {
	releases []Release
	files    map[string]string // "tag/name" → contents
}

func (f *fakeSource) Releases(context.Context) ([]Release, error) { return f.releases, nil }

func (f *fakeSource) Download(_ context.Context, tag, name string, limit int64, w io.Writer) error {
	body, ok := f.files[tag+"/"+name]
	if !ok {
		return fmt.Errorf("%s/%s: 404", tag, name)
	}
	if int64(len(body)) > limit {
		return errors.New("too large")
	}
	_, err := io.WriteString(w, body)
	return err
}

var asset = "raptor_linux_" + runtime.GOARCH

// publish adds a signed release whose binary is a script printing version.
func (f *fakeSource) publish(priv minisign.PrivateKey, tag, version string) {
	bin := "#!/bin/sh\necho '" + version + " (abc1234, 2026-01-01)'\n"
	sum := sha256.Sum256([]byte(bin))
	sums := hex.EncodeToString(sum[:]) + "  " + asset + "\n"
	f.releases = append(f.releases, Release{Tag: tag})
	f.files[tag+"/"+asset] = bin
	f.files[tag+"/checksums.txt"] = sums
	f.files[tag+"/checksums.txt.minisig"] = string(minisign.SignWithComments(priv, []byte(sums), "raptor "+tag+" checksums.txt", "sig"))
}

type updaterEnv struct {
	u        *Updater
	src      *fakeSource
	priv     minisign.PrivateKey
	restarts *atomic.Int32
}

func newUpdater(t *testing.T, current string) updaterEnv {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	layout := Layout{Dir: dir}
	if err := os.WriteFile(layout.Binary(current), nil, 0o755); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}
	if err := layout.Promote(current); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{files: map[string]string{}}
	restarts := &atomic.Int32{}
	u := &Updater{
		Layout: layout, Source: src, Key: pub,
		StatePath: filepath.Join(t.TempDir(), "update.json"),
		Channel:   "stable", Current: current,
		Restart: func() { restarts.Add(1) },
		Log:     slog.New(slog.DiscardHandler),

		executable:   func() (string, error) { return filepath.EvalSymlinks(layout.Binary(current)) },
		underSystemd: func() bool { return true },
	}
	return updaterEnv{u, src, priv, restarts}
}

func TestCheck(t *testing.T) {
	e := newUpdater(t, "1.1.0")
	e.src.publish(e.priv, "v1.0.0", "1.0.0")
	e.src.publish(e.priv, "v1.1.0", "1.1.0")
	e.src.publish(e.priv, "v1.2.0-rc.1", "1.2.0-rc.1")
	e.src.releases[2].Prerelease = true
	ctx := context.Background()

	for _, tc := range []struct {
		channel, pin, version, current string
		want                           Plan
	}{
		{channel: "stable", want: Plan{Target: "1.1.0", Channel: "stable"}},
		{channel: "beta", want: Plan{Target: "1.2.0-rc.1", Channel: "beta", Available: true}},
		// Never backwards from a channel, even from a newer beta.
		{channel: "stable", current: "1.2.0-rc.2", want: Plan{Target: "1.2.0-rc.2", Channel: "stable"}},
		// A pin or a request can go anywhere, including back.
		{channel: "beta", pin: "1.0.0", want: Plan{Target: "1.0.0", Channel: "pin", Available: true}},
		{pin: "1.0.0", version: "v1.1.0", want: Plan{Target: "1.1.0", Channel: "requested"}},
		{version: "1.0.0", want: Plan{Target: "1.0.0", Channel: "requested", Available: true}},
	} {
		e.u.Channel, e.u.Pin, e.u.Current = tc.channel, tc.pin, "1.1.0"
		if tc.current != "" {
			e.u.Current = tc.current
		}
		tc.want.Current = e.u.Current
		if got, err := e.u.Check(ctx, tc.version); err != nil || got != tc.want {
			t.Errorf("%+v: got %+v, %v", tc, got, err)
		}
	}

	e.u.Channel, e.u.Pin, e.u.Current = "stable", "", "dev"
	if _, err := e.u.Check(ctx, ""); err == nil || !strings.Contains(err.Error(), "development build") {
		t.Errorf("dev build: %v", err)
	}
	if p, err := e.u.Check(ctx, "1.1.0"); err != nil || !p.Available {
		t.Errorf("dev build, given a version: %+v %v", p, err)
	}
	if _, err := e.u.Check(ctx, "../../etc"); !errors.Is(err, errBadVersion) {
		t.Errorf("bad version: %v", err)
	}
}

func TestApply(t *testing.T) {
	e := newUpdater(t, "1.0.0")
	e.src.publish(e.priv, "v1.1.0", "1.1.0")
	ctx := context.Background()

	p, err := e.u.Apply(ctx, "", "local:root")
	if err != nil || p.Target != "1.1.0" || !p.Available {
		t.Fatalf("%+v %v", p, err)
	}
	st, _ := ReadState(e.u.StatePath)
	if st.Status != StatusTrial || st.From != "1.0.0" || st.To != "1.1.0" || st.Actor != "local:root" {
		t.Fatalf("%+v", st)
	}
	fi, err := os.Stat(e.u.Layout.Binary("1.1.0"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("installed: %v %v", fi, err)
	}
	if cur, _ := e.u.Layout.Current(); cur != "1.0.0" {
		t.Fatalf("current moved before the trial: %s", cur)
	}
	waitFor(t, func() bool { return e.restarts.Load() == 1 })

	// Busy until the restart: the trial is pending.
	if _, err := e.u.Apply(ctx, "", "local:root"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second apply: %v", err)
	}
}

func TestApplyUpToDate(t *testing.T) {
	e := newUpdater(t, "1.1.0")
	e.src.publish(e.priv, "v1.1.0", "1.1.0")
	p, err := e.u.Apply(context.Background(), "", "local:root")
	if err != nil || p.Available || e.restarts.Load() != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	if st, _ := ReadState(e.u.StatePath); st.Status != "" {
		t.Fatalf("state written: %+v", st)
	}
	// Not busy afterwards.
	if _, err := e.u.Apply(context.Background(), "", "local:root"); err != nil {
		t.Fatal(err)
	}
}

func TestApplyRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(e updaterEnv)
		want  string
	}{
		"tampered binary": {func(e updaterEnv) {
			e.src.files["v1.1.0/"+asset] += "evil\n"
		}, "doesn't match its signed checksum"},
		"signed by another key": {func(e updaterEnv) {
			_, other, _ := minisign.GenerateKey(rand.Reader)
			e.src.publish(other, "v1.1.0", "1.1.0")
		}, "not made with the release key"},
		"signature from another release": {func(e updaterEnv) {
			e.src.publish(e.priv, "v1.0.5", "1.0.5")
			e.src.files["v1.1.0/checksums.txt"] = e.src.files["v1.0.5/checksums.txt"]
			e.src.files["v1.1.0/checksums.txt.minisig"] = e.src.files["v1.0.5/checksums.txt.minisig"]
		}, "signed for"},
		"no binary for this arch": {func(e updaterEnv) {
			sums := strings.Replace(e.src.files["v1.1.0/checksums.txt"], asset, "raptor_linux_mips", 1)
			e.src.files["v1.1.0/checksums.txt"] = sums
			e.src.files["v1.1.0/checksums.txt.minisig"] = string(minisign.SignWithComments(e.priv, []byte(sums), "raptor v1.1.0 checksums.txt", ""))
		}, "has no " + asset},
		"binary claims another version": {func(e updaterEnv) {
			e.src.publish(e.priv, "v1.1.0", "1.0.9")
		}, `says it's version "1.0.9"`},
		"missing signature": {func(e updaterEnv) {
			delete(e.src.files, "v1.1.0/checksums.txt.minisig")
		}, "404"},
		"not installed in the layout": {func(e updaterEnv) {
			e.u.executable = func() (string, error) { return "/home/me/raptor", nil }
		}, "not from"},
		"not under systemd": {func(e updaterEnv) {
			e.u.underSystemd = func() bool { return false }
		}, "systemd"},
		"trial pending": {func(e updaterEnv) {
			_ = WriteState(e.u.StatePath, State{Status: StatusTrial, To: "1.0.8"})
		}, "1.0.8 is on trial"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newUpdater(t, "1.0.0")
			e.src.publish(e.priv, "v1.1.0", "1.1.0")
			tc.setup(e)
			before, _ := ReadState(e.u.StatePath)
			_, err := e.u.Apply(context.Background(), "1.1.0", "local:root")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(e.u.Layout.Binary("1.1.0")); err == nil {
				t.Fatal("installed anyway")
			}
			if after, _ := ReadState(e.u.StatePath); after != before {
				t.Fatalf("state changed: %+v", after)
			}
			entries, _ := os.ReadDir(e.u.Layout.Dir)
			for _, en := range entries {
				if strings.HasPrefix(en.Name(), tmpPrefix) {
					t.Fatalf("left %s behind", en.Name())
				}
			}
			time.Sleep(10 * time.Millisecond)
			if e.restarts.Load() != 0 {
				t.Fatal("restarted")
			}
		})
	}
}

func TestPrune(t *testing.T) {
	l := Layout{Dir: t.TempDir()}
	for _, name := range []string{"raptor-1.0.0", "raptor-1.1.0", "raptor-1.2.0", ".tmp-raptor-123", "config"} {
		if err := os.WriteFile(filepath.Join(l.Dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Promote("1.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := l.Prune("1.2.0"); err != nil {
		t.Fatal(err)
	}
	var left []string
	entries, _ := os.ReadDir(l.Dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if got := strings.Join(left, " "); got != "config current raptor-1.1.0 raptor-1.2.0" {
		t.Fatal(got)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}
