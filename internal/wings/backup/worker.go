package backup

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
)

// Backups run in a worker process (`raptor wings backup-worker`), not in
// Wings: Wings is protected from the OOM killer, so Kopia's memory use
// inside it would make the kernel kill game servers instead; a separate
// process can be given low CPU and I/O weight and a memory limit of its
// own; and a crash in it can't take Wings down (docs/DECISIONS.md).

// Worker operations.
const (
	opSnapshot = "snapshot"
	opRestore  = "restore"
	opDelete   = "delete"
	opMaintain = "maintain"
	opBrowse   = "browse"
	opExtract  = "extract"
	opTest     = "test" // write, read, and remove a small file
	opSize     = "size" // add up what the repository stores
	opList     = "list" // the server backups in the repository
)

// request is one operation for the worker, sent on its stdin.
type request struct {
	Op       string                  `json:"op"`
	Dest     engine.Destination      `json:"dest"`
	Password string                  `json:"password"`
	StateDir string                  `json:"state_dir"`
	Snapshot *engine.SnapshotRequest `json:"snapshot,omitempty"`
	Restore  *engine.RestoreRequest  `json:"restore,omitempty"`
	Delete   []string                `json:"delete,omitempty"`
	Browse   *engine.BrowseRequest   `json:"browse,omitempty"`
	Extract  *engine.ExtractRequest  `json:"extract,omitempty"`
}

// message is a line the worker writes on its stdout: progress, then one
// final result or error.
type message struct {
	Progress *engine.Progress `json:"progress,omitempty"`
	Result   json.RawMessage  `json:"result,omitempty"`
	Error    string           `json:"error,omitempty"`
	TooLarge bool             `json:"too_large,omitempty"`
	Done     bool             `json:"done,omitempty"`
}

func execute(ctx context.Context, req request, progress func(engine.Progress)) (any, error) {
	e := &engine.Engine{Dest: req.Dest, Password: req.Password, StateDir: req.StateDir}
	switch req.Op {
	case opSnapshot:
		if req.Snapshot == nil {
			break
		}
		return e.Snapshot(ctx, *req.Snapshot, progress)
	case opRestore:
		if req.Restore == nil {
			break
		}
		return e.Restore(ctx, *req.Restore, progress)
	case opDelete:
		return nil, e.Delete(ctx, req.Delete)
	case opMaintain:
		return nil, e.Maintain(ctx)
	case opBrowse:
		if req.Browse == nil {
			break
		}
		return e.Browse(ctx, *req.Browse)
	case opExtract:
		if req.Extract == nil {
			break
		}
		return e.Extract(ctx, *req.Extract, progress)
	case opTest:
		return nil, e.Test(ctx)
	case opSize:
		return e.Size(ctx)
	case opList:
		return e.List(ctx)
	}
	return nil, fmt.Errorf("bad worker request %q", req.Op)
}

// Serve runs one request read from r, writing messages to w. It's the
// backup worker's main.
func Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	var req request
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	enc := json.NewEncoder(w)
	res, err := execute(ctx, req, func(p engine.Progress) { _ = enc.Encode(message{Progress: &p}) })
	final := message{Done: true}
	if err != nil {
		final.Error, final.TooLarge = err.Error(), errors.Is(err, engine.ErrTooLarge)
	} else if res != nil {
		if final.Result, err = json.Marshal(res); err != nil {
			return err
		}
	}
	return enc.Encode(final)
}

// Runner runs worker requests.
type Runner interface {
	// Run runs req and returns its JSON result. Worker diagnostics go to log.
	Run(ctx context.Context, req request, progress func(engine.Progress), log io.Writer) (json.RawMessage, error)
}

// InProcess runs requests in the calling process (tests, and hosts without
// systemd).
type InProcess struct{}

// Run implements Runner.
func (InProcess) Run(ctx context.Context, req request, progress func(engine.Progress), _ io.Writer) (json.RawMessage, error) {
	res, err := execute(ctx, req, progress)
	if err != nil || res == nil {
		return nil, err
	}
	return json.Marshal(res)
}

// Process runs each request in a new worker process.
type Process struct {
	// Command is the worker's argv, e.g. {"/usr/local/bin/raptor", "wings",
	// "backup-worker"}.
	Command []string
	// Slice, if set, runs the worker in a systemd scope in this slice with
	// low CPU and I/O weight and MemoryMax, via systemd-run.
	Slice     string
	MemoryMax int64
	Env       []string // extra environment (tests)
}

// Worker scope weights: the lowest that still makes progress when game
// servers (weight 100) are busy. As with install containers, the I/O weight
// only applies with the BFQ scheduler or io.cost (docs/WINGS.md#resource-limits).
const workerWeight = 10

// Run implements Runner.
func (p Process) Run(ctx context.Context, req request, progress func(engine.Progress), log io.Writer) (json.RawMessage, error) {
	argv := p.Command
	if p.Slice != "" {
		argv = append([]string{
			"systemd-run", "--scope", "--quiet", "--collect", "--slice=" + p.Slice,
			"-p", "CPUWeight=" + strconv.Itoa(workerWeight), "-p", "IOWeight=" + strconv.Itoa(workerWeight),
			"-p", "MemoryMax=" + strconv.FormatInt(p.MemoryMax, 10), "--",
		}, argv...)
	}
	in, err := json.Marshal(req) //nolint:gosec // sent to the worker on its stdin
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // our own binary
	cmd.Env = append(os.Environ(), p.Env...)
	cmd.Stdin = bytesReader(in)
	cmd.Stderr = log
	cmd.SysProcAttr = sysProcAttr() // dies with Wings
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 30 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start backup worker: %w", err)
	}
	var final *message
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch {
		case m.Progress != nil && progress != nil:
			progress(*m.Progress)
		case m.Done:
			final = &m
		}
	}
	waitErr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return nil, context.Cause(ctx)
	case final == nil:
		return nil, fmt.Errorf("backup worker exited without a result: %w", waitErr)
	case final.TooLarge:
		return nil, fmt.Errorf("%w: %s", engine.ErrTooLarge, final.Error)
	case final.Error != "":
		return nil, errors.New(final.Error)
	}
	return final.Result, nil
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
