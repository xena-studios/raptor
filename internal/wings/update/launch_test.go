package update

import (
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary doubles as the new version on trial: with fakeEnv set it
// behaves as the mode says instead of running tests.
const fakeEnv = "RAPTOR_TEST_FAKE_WINGS"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		os.Exit(fakeWings(mode))
	}
	os.Exit(m.Run())
}

func fakeWings(mode string) int {
	l := &Launcher{Log: slog.New(slog.DiscardHandler)}
	if _, handled := l.Run(); handled || trialPipe == nil {
		return 90 // not started as a trial
	}
	if os.Getenv(trialEnv) != "" {
		return 91 // leaked to what the trial starts
	}
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	switch mode {
	case "healthy": // ready, then runs until stopped
		Ready()
		<-term
		return 0
	case "healthy-exit": // ready, then exits by itself a bit later
		Ready()
		time.Sleep(500 * time.Millisecond)
		return 7
	case "crash":
		return 3
	case "crash-after-ready": // before the settle time is over
		Ready()
		time.Sleep(20 * time.Millisecond)
		return 4
	case "hang": // never ready; stops when asked
		<-term
		return 0
	}
	return 92
}

type launchEnv struct {
	layout Layout
	state  string
	l      *Launcher
}

// newLaunch sets up a layout with version 1.0.0 current and 2.0.0 (the test
// binary) staged for a trial.
func newLaunch(t *testing.T, mode string) launchEnv {
	t.Helper()
	dir := t.TempDir()
	layout := Layout{Dir: dir}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for v, src := range map[string]string{"1.0.0": "", "2.0.0": self} {
		if src == "" {
			if err := os.WriteFile(layout.Binary(v), []byte("old"), 0o755); err != nil { //nolint:gosec // test
				t.Fatal(err)
			}
			continue
		}
		if err := os.Symlink(src, layout.Binary(v)); err != nil {
			t.Fatal(err)
		}
	}
	if err := layout.Promote("1.0.0"); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "update.json")
	if err := WriteState(state, State{Status: StatusTrial, From: "1.0.0", To: "2.0.0", Started: time.Now()}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeEnv, mode)
	return launchEnv{layout, state, &Launcher{
		Layout: layout, StatePath: state, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		timeout: 3 * time.Second, settle: 200 * time.Millisecond,
	}}
}

func (e launchEnv) check(t *testing.T, status, current string, staged bool) State {
	t.Helper()
	st, err := ReadState(e.state)
	if err != nil || st.Status != status {
		t.Fatalf("state %+v, %v; want %s", st, err, status)
	}
	if cur, _ := e.layout.Current(); cur != current {
		t.Fatalf("current = %s, want %s", cur, current)
	}
	if _, err := os.Lstat(e.layout.Binary("2.0.0")); (err == nil) != staged {
		t.Fatalf("2.0.0 installed: %v, want %v", err == nil, staged)
	}
	return st
}

func TestLaunchPromotesHealthy(t *testing.T) {
	e := newLaunch(t, "healthy-exit")
	code, handled := e.l.Run()
	if !handled || code != 7 {
		t.Fatalf("code %d handled %v", code, handled)
	}
	st := e.check(t, StatusSucceeded, "2.0.0", true)
	if st.Attempts != 1 || st.Finished.IsZero() || st.Error != "" {
		t.Fatalf("%+v", st)
	}
	// The next start runs 2.0.0 directly.
	if _, handled := e.l.Run(); handled {
		t.Fatal("ran another trial")
	}
}

func TestLaunchRollsBack(t *testing.T) {
	for mode, reason := range map[string]string{
		"crash":             "exited before becoming healthy: exit status 3",
		"crash-after-ready": "exit status 4",
		"hang":              "not healthy within",
	} {
		t.Run(mode, func(t *testing.T) {
			e := newLaunch(t, mode)
			e.l.timeout = time.Second
			start := time.Now()
			if _, handled := e.l.Run(); handled {
				t.Fatal("handled: the current version should run instead")
			}
			st := e.check(t, StatusFailed, "1.0.0", false)
			if !strings.Contains(st.Error, reason) {
				t.Fatalf("error %q, want %q", st.Error, reason)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("took too long")
			}
		})
	}
}

func TestLaunchGivesUpAfterAttempts(t *testing.T) {
	e := newLaunch(t, "healthy")
	st, _ := ReadState(e.state)
	st.Attempts = maxAttempts
	if err := WriteState(e.state, st); err != nil {
		t.Fatal(err)
	}
	if _, handled := e.l.Run(); handled {
		t.Fatal("ran a trial")
	}
	st = e.check(t, StatusFailed, "1.0.0", false)
	if !strings.Contains(st.Error, "3 starts") {
		t.Fatal(st.Error)
	}
}

func TestLaunchStoppedDuringTrial(t *testing.T) {
	e := newLaunch(t, "hang")
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
	// Keep the test process alive through the SIGTERM.
	signal.Ignore(syscall.SIGTERM)
	defer signal.Reset(syscall.SIGTERM)
	code, handled := e.l.Run()
	if !handled || code != 0 {
		t.Fatalf("code %d handled %v", code, handled)
	}
	// Still on trial; the next start tries again.
	st := e.check(t, StatusTrial, "1.0.0", true)
	if st.Attempts != 1 {
		t.Fatal(st.Attempts)
	}
}

func TestLaunchNothingToDo(t *testing.T) {
	e := newLaunch(t, "healthy")
	for _, st := range []State{{}, {Status: StatusSucceeded, To: "2.0.0"}, {Status: StatusFailed, To: "2.0.0"}} {
		if err := WriteState(e.state, st); err != nil {
			t.Fatal(err)
		}
		if _, handled := e.l.Run(); handled {
			t.Fatalf("%+v: ran a trial", st)
		}
	}
	// A corrupt state file doesn't stop Wings from starting.
	if err := os.WriteFile(e.state, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, handled := e.l.Run(); handled {
		t.Fatal("ran a trial")
	}
}

func TestLaunchPromoteFails(t *testing.T) {
	e := newLaunch(t, "healthy")
	// "current" can't be replaced: a directory is in the way of the rename.
	if err := os.Remove(filepath.Join(e.layout.Dir, currentLnk)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.layout.Dir, currentLnk, "x"), 0o755); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}
	if _, handled := e.l.Run(); handled {
		t.Fatal("handled: the current version should run instead")
	}
	st, _ := ReadState(e.state)
	if st.Status != StatusFailed || !strings.Contains(st.Error, "can't switch to it") {
		t.Fatalf("%+v", st)
	}
}
