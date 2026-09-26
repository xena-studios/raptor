package install

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
)

// fakeRuntime records what Run asks of the runtime.
type fakeRuntime struct {
	containers.Runtime
	badArch  string
	installs []containers.InstallSpec
	output   string
	exit     int64
	err      error
}

func (f *fakeRuntime) CheckArch(_ context.Context, image string) error {
	if image == f.badArch {
		return containers.ErrUnsupportedArch
	}
	return nil
}

func (f *fakeRuntime) Install(_ context.Context, s containers.InstallSpec) (containers.InstallResult, error) {
	f.installs = append(f.installs, s)
	_, _ = io.WriteString(s.Output, f.output)
	return containers.InstallResult{ExitCode: f.exit}, f.err
}

func testEgg() *eggs.Egg {
	e := &eggs.Egg{
		Install:   eggs.Install{Script: "echo hi", Container: "installer:1", Entrypoint: "bash"},
		Variables: []eggs.Variable{{Env: "VERSION", Default: "latest", Rules: []string{"required", "string"}}},
	}
	e.Raptor.Install.Timeout = "90m"
	return e
}

func params(t *testing.T, e *eggs.Egg) Params {
	dir := filepath.Join(t.TempDir(), "srv")
	return Params{
		Egg: e, Image: "game:1", ServerID: "s1", Dir: dir, TmpDir: t.TempDir(),
		Env: eggs.Runtime{MemoryMiB: 1024, Port: 25565}, UID: os.Getuid(), GID: os.Getgid(),
	}
}

func TestRun(t *testing.T) {
	rt := &fakeRuntime{output: "installing\n", exit: 1}
	p := params(t, testEgg())
	p.Variables = map[string]string{"VERSION": "1.21"}
	res, err := Run(context.Background(), rt, p)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 1 || string(res.Log) != "installing\n" || res.Variables["VERSION"] != "1.21" {
		t.Fatalf("result = %+v", res)
	}
	s := rt.installs[0]
	if s.Timeout != 90*time.Minute || s.MemoryMiB != 1024 || !contains(s.Env, "VERSION=1.21") || !contains(s.Env, "SERVER_PORT=25565") {
		t.Fatalf("spec = %+v", s)
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Fatalf("server dir not created: %v", err)
	}
}

func TestRunFailsEarly(t *testing.T) {
	// Invalid variables: nothing runs.
	rt := &fakeRuntime{}
	p := params(t, testEgg())
	p.Variables = map[string]string{"VERSION": ""}
	var ve eggs.VariableErrors
	if _, err := Run(context.Background(), rt, p); !errors.As(err, &ve) || len(rt.installs) != 0 {
		t.Fatalf("got %v, installs %d", err, len(rt.installs))
	}

	// The install image isn't available for this CPU.
	rt = &fakeRuntime{badArch: "installer:1"}
	if _, err := Run(context.Background(), rt, params(t, testEgg())); !errors.Is(err, containers.ErrUnsupportedArch) || len(rt.installs) != 0 {
		t.Fatalf("got %v", err)
	}

	// The egg declares other architectures only.
	e := testEgg()
	e.Raptor.Arch = []string{"riscv64"}
	if _, err := Run(context.Background(), &fakeRuntime{}, params(t, e)); !errors.Is(err, containers.ErrUnsupportedArch) {
		t.Fatalf("got %v", err)
	}
}

func TestRunSkip(t *testing.T) {
	rt := &fakeRuntime{}
	p := params(t, testEgg())
	p.Skip = true
	res, err := Run(context.Background(), rt, p)
	if err != nil || !res.Skipped || len(rt.installs) != 0 {
		t.Fatalf("res=%+v err=%v installs=%d", res, err, len(rt.installs))
	}
}

func TestRunDockerError(t *testing.T) {
	rt := &fakeRuntime{err: errors.New("install timed out after 2h0m0s")}
	_, err := Run(context.Background(), rt, params(t, testEgg()))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v", err)
	}
}

func TestTail(t *testing.T) {
	tl := &tail{limit: 10}
	for range 5 {
		_, _ = tl.Write([]byte("0123456789"))
	}
	_, _ = tl.Write([]byte("abc"))
	got, truncated := tl.bytes()
	if string(got) != "3456789abc" || !truncated {
		t.Fatalf("got %q, %v", got, truncated)
	}
	small := &tail{limit: 10}
	_, _ = small.Write([]byte("hi"))
	if got, truncated := small.bytes(); string(got) != "hi" || truncated {
		t.Fatalf("got %q, %v", got, truncated)
	}
}

func TestFixOwnershipDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := FixOwnership(dir, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
}

func contains(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
