package update

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// The trial protocol between the running version (the launcher) and the one
// on trial. It must never change: every version is started by an older one.
const (
	trialEnv = "RAPTOR_UPDATE_TRIAL" // "1" in the trial's environment
	trialFD  = 3                     // a pipe to the launcher
	readyMsg = "ready\n"             // written to it once the trial is healthy
)

// Trial timings (docs/WINGS.md#updates). Variables only so the e2e build
// (e2e.go) can shorten them.
var (
	// TrialTimeout is how long a new version has to become healthy.
	TrialTimeout = 5 * time.Minute
	// settleTime is how long it must keep running after that before it's
	// made current, so a crash right after start still rolls back.
	settleTime = 30 * time.Second
)

// Trial limits.
const (
	// maxAttempts bounds trial starts, counted before each one: a version
	// that takes the whole box down (so the launcher never sees it fail)
	// is given up on after this many boots.
	maxAttempts = 3
	// stopTimeout is how long a failed trial gets to stop, within
	// raptor-wings.service's TimeoutStopSec.
	stopTimeout = 25 * time.Second
)

// Launcher starts a new version on trial. It runs in the version that's
// current, so a broken new version can't prevent its own rollback: the trial
// is this process's child, and "current" only moves to it once it's healthy.
type Launcher struct {
	Layout    Layout
	StatePath string
	Args      []string // what to run the trial with: this process's arguments
	Log       *slog.Logger

	timeout, settle time.Duration // tests shorten these
}

// Run is called first thing by `raptor wings run`. If an update is waiting
// for its trial, it runs the new version as a child and supervises it, and
// returns handled with the code to exit with (systemd then starts Wings
// again). Otherwise, including after a trial failed, the caller runs Wings
// itself.
func (l *Launcher) Run() (code int, handled bool) {
	if os.Getenv(trialEnv) != "" {
		startTrial()
		return 0, false
	}
	st, err := ReadState(l.StatePath)
	if err != nil {
		l.Log.Error("can't read the update state; starting this version", "path", l.StatePath, "err", err)
		return 0, false
	}
	if st.Status != StatusTrial {
		return 0, false
	}
	log := l.Log.With("from", st.From, "to", st.To)
	if st.Attempts >= maxAttempts {
		l.fail(&st, fmt.Sprintf("didn't become healthy in %d starts", st.Attempts), log)
		return 0, false
	}
	st.Attempts++
	if err := WriteState(l.StatePath, st); err != nil {
		// Without the attempt counted, a version that hangs the box could
		// be retried forever.
		log.Error("can't record the update attempt; starting this version", "err", err)
		return 0, false
	}
	return l.trial(&st, log)
}

func (l *Launcher) trial(st *State, log *slog.Logger) (int, bool) {
	timeout, settle := l.timeout, l.settle
	if timeout == 0 {
		timeout, settle = TrialTimeout, settleTime
	}

	r, w, err := os.Pipe()
	if err != nil {
		l.fail(st, "can't start it: "+err.Error(), log)
		return 0, false
	}
	// No context: the trial is stopped with signals, which it handles.
	cmd := exec.Command(l.Layout.Binary(st.To), l.Args...) //nolint:gosec,noctx // an installed, verified version
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), trialEnv+"=1")
	cmd.ExtraFiles = []*os.File{w} // fd 3
	err = cmd.Start()
	_ = w.Close()
	if err != nil {
		_ = r.Close()
		l.fail(st, "can't start it: "+err.Error(), log)
		return 0, false
	}
	log.Info("starting the new version on trial", "attempt", st.Attempts, "pid", cmd.Process.Pid, "timeout", timeout.String())

	ready := make(chan struct{})
	go func() {
		defer func() { _ = r.Close() }()
		buf := make([]byte, len(readyMsg))
		if _, err := io.ReadFull(r, buf); err == nil && string(buf) == readyMsg {
			close(ready)
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	var settled <-chan time.Time
	promoted, stopping := false, false
	for {
		select {
		case <-ready:
			ready = nil
			settled = time.After(settle)
			log.Info("the new version is healthy; confirming", "in", settle.String())

		case <-settled:
			settled = nil
			if stopping {
				continue
			}
			deadline.Stop()
			if err := l.Layout.Promote(st.To); err != nil {
				l.fail(st, "can't switch to it: "+err.Error(), log)
				stop(cmd, exited)
				return 0, false
			}
			promoted = true
			st.Status, st.Finished, st.Error = StatusSucceeded, time.Now(), ""
			if err := WriteState(l.StatePath, *st); err != nil {
				log.Error("can't record the update", "err", err)
			}
			if err := l.Layout.Prune(st.From, st.To); err != nil {
				log.Warn("can't remove old versions", "err", err)
			}
			log.Info("updated")

		case <-deadline.C:
			if promoted || stopping {
				continue
			}
			l.fail(st, fmt.Sprintf("not healthy within %s", timeout), log)
			stop(cmd, exited)
			return 0, false

		case err := <-exited:
			switch {
			case promoted:
				// Stopped or restarting: systemd starts the new version
				// directly from now on.
				return exitCode(err), true
			case stopping:
				log.Info("stopped during the trial; it continues on the next start")
				return 0, true
			}
			l.fail(st, "it exited before becoming healthy: "+describe(err), log)
			return 0, false

		case sig := <-sigs:
			// systemd signals the whole cgroup, but something may signal
			// only the main process.
			stopping = true
			_ = cmd.Process.Signal(sig)
		}
	}
}

// fail records a failed update and removes the new version. The caller then
// carries on with the current one.
func (l *Launcher) fail(st *State, reason string, log *slog.Logger) {
	st.Status, st.Finished, st.Error = StatusFailed, time.Now(), reason
	log.Error("update failed; rolling back to the previous version", "err", reason)
	if err := WriteState(l.StatePath, *st); err != nil {
		log.Error("can't record the failed update", "err", err)
	}
	if cur, err := l.Layout.Current(); err != nil || cur != st.To {
		if err := os.Remove(l.Layout.Binary(st.To)); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warn("can't remove the failed version", "err", err)
		}
	}
}

// stop stops a trial that didn't become healthy.
func stop(cmd *exec.Cmd, exited <-chan error) {
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(stopTimeout):
		_ = cmd.Process.Kill()
		<-exited
	}
}

func exitCode(err error) int {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee) && ee.ExitCode() > 0:
		return ee.ExitCode()
	}
	return 1
}

func describe(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

// The trial's side.
var (
	trialPipe *os.File
	readyOnce sync.Once
)

func startTrial() {
	_ = os.Unsetenv(trialEnv)    // not for anything Wings starts
	syscall.CloseOnExec(trialFD) // same for the pipe
	trialPipe = os.NewFile(trialFD, "update-trial")
}

// Ready tells the launcher that this version, on trial, is healthy: Wings is
// serving its local API and the container runtime is up. Outside a trial it
// does nothing.
func Ready() {
	readyOnce.Do(func() {
		if trialPipe != nil {
			_, _ = trialPipe.WriteString(readyMsg)
			_ = trialPipe.Close()
		}
	})
}
