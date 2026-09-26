package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/store"
)

type env struct {
	db   *store.DB
	logs string
}

func newEnv(t *testing.T) env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return env{db: db, logs: filepath.Join(dir, "jobs")}
}

func (v env) engine(t *testing.T, register func(*Engine)) *Engine {
	t.Helper()
	e := New(Options{Store: v.db, LogDir: v.logs, Limits: map[string]int{"install": 2}, BackoffBase: 50 * time.Millisecond, Poll: 50 * time.Millisecond})
	register(e)
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

func wait(t *testing.T, e *Engine, id string) Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	j, err := e.Wait(ctx, id)
	if err != nil {
		t.Fatalf("waiting for %s: %v", id, err)
	}
	return j
}

func TestSuccessAndLog(t *testing.T) {
	v := newEnv(t)
	e := v.engine(t, func(e *Engine) {
		e.Register("echo", Handler{Run: func(_ context.Context, j Job, log io.Writer) (any, error) {
			var p struct{ Msg string }
			if err := j.Decode(&p); err != nil {
				return nil, err
			}
			fmt.Fprintln(log, "hello", p.Msg)
			return map[string]string{"got": p.Msg}, nil
		}})
	})
	defer e.Close()
	id, err := e.Enqueue(context.Background(), Spec{Type: "echo", ServerID: "s1", Payload: map[string]string{"Msg": "world"}})
	if err != nil {
		t.Fatal(err)
	}
	j := wait(t, e, id)
	if j.Status != Succeeded || string(j.Result) != `{"got":"world"}` || j.Attempts != 1 {
		t.Fatalf("job = %+v", j)
	}
	if b, _ := e.Log(id); string(b) != "hello world\n" {
		t.Fatalf("log = %q", b)
	}
	if list, _ := e.List(context.Background(), "s1", 10); len(list) != 1 {
		t.Fatalf("list = %v", list)
	}
}

func TestRetryableAndFailure(t *testing.T) {
	v := newEnv(t)
	var calls atomic.Int32
	e := v.engine(t, func(e *Engine) {
		e.Register("flaky", Handler{MaxAttempts: 3, Run: func(context.Context, Job, io.Writer) (any, error) {
			if calls.Add(1) < 3 {
				return nil, Retryable(errors.New("registry timeout"))
			}
			return nil, nil
		}})
		e.Register("broken", Handler{MaxAttempts: 3, Run: func(context.Context, Job, io.Writer) (any, error) {
			return nil, errors.New("bad config") // not retryable
		}})
		e.Register("panics", Handler{Run: func(context.Context, Job, io.Writer) (any, error) { panic("oops") }})
	})
	defer e.Close()
	ctx := context.Background()

	id, _ := e.Enqueue(ctx, Spec{Type: "flaky"})
	if j := wait(t, e, id); j.Status != Succeeded || j.Attempts != 3 {
		t.Fatalf("flaky: %+v", j)
	}
	id, _ = e.Enqueue(ctx, Spec{Type: "broken"})
	if j := wait(t, e, id); j.Status != Failed || j.Attempts != 1 || j.Error != "bad config" {
		t.Fatalf("broken: %+v", j)
	}
	id, _ = e.Enqueue(ctx, Spec{Type: "panics"})
	if j := wait(t, e, id); j.Status != Failed || !strings.Contains(j.Error, "panicked") {
		t.Fatalf("panic: %+v", j)
	}
	if _, err := e.Enqueue(ctx, Spec{Type: "nope"}); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("unknown type: %v", err)
	}
}

// Server-locked jobs for one server never overlap; a class never exceeds its
// limit.
func TestLocksAndLimits(t *testing.T) {
	v := newEnv(t)
	var mu sync.Mutex
	perServer := map[string]int{}
	inClass, maxServer, maxClass := 0, 0, 0
	e := v.engine(t, func(e *Engine) {
		e.Register("install", Handler{Class: "install", ServerLock: true, Run: func(_ context.Context, j Job, _ io.Writer) (any, error) {
			mu.Lock()
			perServer[j.ServerID]++
			inClass++
			maxServer = max(maxServer, perServer[j.ServerID])
			maxClass = max(maxClass, inClass)
			mu.Unlock()
			time.Sleep(50 * time.Millisecond)
			mu.Lock()
			perServer[j.ServerID]--
			inClass--
			mu.Unlock()
			return nil, nil
		}})
	})
	defer e.Close()
	var ids []string
	for i := range 12 {
		id, err := e.Enqueue(context.Background(), Spec{Type: "install", ServerID: fmt.Sprint("s", i%3)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if j := wait(t, e, id); j.Status != Succeeded {
			t.Fatalf("%+v", j)
		}
	}
	if maxServer != 1 || maxClass != 2 {
		t.Fatalf("max per server %d (want 1), max in class %d (want 2)", maxServer, maxClass)
	}
}

func TestCancel(t *testing.T) {
	v := newEnv(t)
	started := make(chan struct{})
	e := v.engine(t, func(e *Engine) {
		e.Register("slow", Handler{Run: func(ctx context.Context, _ Job, _ io.Writer) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}})
		e.Register("later", Handler{Run: func(context.Context, Job, io.Writer) (any, error) { return nil, nil }})
	})
	defer e.Close()
	ctx := context.Background()
	id, _ := e.Enqueue(ctx, Spec{Type: "slow"})
	<-started
	if err := e.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if j := wait(t, e, id); j.Status != Cancelled {
		t.Fatalf("running cancel: %+v", j)
	}
	queued, _ := e.Enqueue(ctx, Spec{Type: "later", RunAfter: time.Now().Add(time.Hour)})
	if err := e.Cancel(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if j, _ := e.Get(ctx, queued); j.Status != Cancelled {
		t.Fatalf("queued cancel: %+v", j)
	}
}

// A Wings stop interrupts running jobs; the next Wings resumes resumable
// ones and fails the rest. Nothing is silently lost.
func TestResumeAfterRestart(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	var runs atomic.Int32
	started := make(chan struct{}, 4)
	register := func(e *Engine) {
		e.Register("install", Handler{Resumable: true, MaxAttempts: 3, Run: func(ctx context.Context, _ Job, _ io.Writer) (any, error) {
			n := runs.Add(1)
			started <- struct{}{}
			if n == 1 {
				<-ctx.Done() // interrupted by the stop
				return nil, ctx.Err()
			}
			return "done", nil
		}})
		e.Register("once", Handler{Run: func(ctx context.Context, _ Job, _ io.Writer) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}})
	}
	e1 := v.engine(t, register)
	resumable, _ := e1.Enqueue(ctx, Spec{Type: "install"})
	once, _ := e1.Enqueue(ctx, Spec{Type: "once"})
	<-started
	<-started
	e1.Close()
	if j, _ := e1.Get(ctx, resumable); j.Status != Running {
		t.Fatalf("after stop: %+v (want it left running for the next start)", j)
	}

	e2 := v.engine(t, register)
	defer e2.Close()
	if j := wait(t, e2, resumable); j.Status != Succeeded || j.Attempts != 2 {
		t.Fatalf("resumed: %+v", j)
	}
	if j, _ := e2.Get(ctx, once); j.Status != Failed || !strings.Contains(j.Error, "interrupted") {
		t.Fatalf("non-resumable: %+v", j)
	}
}

func TestLogLimit(t *testing.T) {
	v := newEnv(t)
	e := v.engine(t, func(e *Engine) {
		e.Register("chatty", Handler{Run: func(_ context.Context, _ Job, log io.Writer) (any, error) {
			line := []byte(strings.Repeat("x", 1023) + "\n")
			for range 25 * 1024 { // 25 MB
				_, _ = log.Write(line)
			}
			_, _ = log.Write([]byte("THE END\n"))
			return nil, nil
		}})
	})
	defer e.Close()
	id, _ := e.Enqueue(context.Background(), Spec{Type: "chatty"})
	wait(t, e, id)
	b, err := e.Log(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > LogLimit+128 || !strings.HasSuffix(string(b), "THE END\n") || !strings.HasPrefix(string(b), "[raptor] earlier output was dropped") {
		t.Fatalf("log is %d bytes, ends %q", len(b), b[len(b)-20:])
	}
}
