//go:build !e2e

// Not in the e2e build, which runs from elsewhere without testdata.

package databases

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// advancedCase is one case in testdata/advanced_cases.json, which the
// web app's tests run too (web/src/lib/db-settings.test.ts): with Want,
// the settings it gives; without, the line it fails on (0: not a line's
// fault).
type advancedCase struct {
	Engine    string      `json:"engine"`
	MemoryMiB int64       `json:"memory_mib"`
	Text      string      `json:"text"`
	Want      *[][]string `json:"want"`
	Line      int         `json:"line"`
}

func TestParseAdvanced(t *testing.T) {
	b, err := os.ReadFile("testdata/advanced_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []advancedCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got, err := parseAdvanced(c.Engine, c.Text, c.MemoryMiB)
		if c.Want == nil {
			var ae AdvancedError
			switch {
			case !errors.Is(err, ErrInvalid):
				t.Errorf("%s %q: %v %v, want an error", c.Engine, c.Text, got, err)
			case c.Line > 0 && (!errors.As(err, &ae) || ae.Line != c.Line):
				t.Errorf("%s %q: %v, want line %d", c.Engine, c.Text, err, c.Line)
			case c.Line == 0 && errors.As(err, &ae):
				t.Errorf("%s %q: %v, want no line", c.Engine, c.Text, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s %q: %v", c.Engine, c.Text, err)
			continue
		}
		var pairs [][]string
		for _, s := range got {
			pairs = append(pairs, []string{s.Name, s.Value})
		}
		if len(pairs) != len(*c.Want) || len(pairs) > 0 && !reflect.DeepEqual(pairs, *c.Want) {
			t.Errorf("%s %q: %v, want %v", c.Engine, c.Text, pairs, *c.Want)
		}
	}
}

// Nothing on the lists names a file or a folder, loads code, or touches
// authentication, TLS, or where the engine listens.
func TestAdvancedRulesAreSafe(t *testing.T) {
	// Words in a name (split at _ and -) that mean it shouldn't be there;
	// a name ending in "file" names one (innodb_log_file_size doesn't).
	bad := []string{
		"dir", "path", "plugin", "plugins", "load", "library", "libraries", "module", "include", "init",
		"ssl", "tls", "password", "auth", "authentication", "grant", "bind", "listen", "addresses", "port",
		"socket", "unixsocket", "secure", "command", "archive", "restore", "preload", "hba", "ident",
		"infile", "rename", "replica", "replicaof", "master", "bin", "general", "skip", "datadir",
		"directory", "acl", "aclfile", "protected", "enable", "dbfilename", "logfile", "requirepass",
	}
	for engine, e := range AdvancedRules() {
		for name, r := range e.Rules {
			words := strings.FieldsFunc(name, func(c rune) bool { return c == '_' || c == '-' })
			for _, w := range words {
				if r.Field == "" && (slices.Contains(bad, w) || strings.HasSuffix(name, "file")) {
					t.Errorf("%s: %s is on the list (%q)", engine, name, w)
				}
			}
			if r.Field != "" {
				continue
			}
			switch r.Kind {
			case kindEnum, kindSet:
				if len(r.Values) == 0 {
					t.Errorf("%s: %s has no values", engine, name)
				}
				for _, v := range r.Values {
					if strings.IndexFunc(v, func(c rune) bool { return !valueChar(c) }) >= 0 {
						t.Errorf("%s: %s's value %q", engine, name, v)
					}
				}
			case kindPattern:
				if strings.ContainsAny(r.Chars, "/ \"'=\\-") {
					t.Errorf("%s: %s's characters %q", engine, name, r.Chars)
				}
			case kindInt, kindReal, kindSize, kindDuration:
				if r.Max <= r.Min {
					t.Errorf("%s: %s's bounds %v..%v", engine, name, r.Min, r.Max)
				}
			case kindBool:
			default:
				t.Errorf("%s: %s's kind %q", engine, name, r.Kind)
			}
			if name != normalName(e.Dialect, name) {
				t.Errorf("%s: %s isn't written the engine's way", engine, name)
			}
		}
	}
}

// What parseAdvanced accepts is safe to put on a command line, whatever
// it's given.
func FuzzParseAdvanced(f *testing.F) {
	b, _ := os.ReadFile("testdata/advanced_cases.json")
	var cases []advancedCase
	_ = json.Unmarshal(b, &cases)
	for _, c := range cases {
		f.Add(c.Engine, c.Text, c.MemoryMiB)
	}
	f.Add("postgres", "work_mem = 4MB\r\nwork_mem=\x00", int64(512))
	f.Add("redis", "hz= 10", int64(256))
	f.Fuzz(func(t *testing.T, engine, text string, mem int64) {
		set, err := parseAdvanced(engine, text, mem)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("an error that isn't ErrInvalid: %v", err)
			}
			return
		}
		rules := advancedRules[engine].Rules
		seen := map[string]bool{}
		for _, s := range set {
			r, ok := rules[s.Name]
			if !ok || r.Field != "" {
				t.Fatalf("%q isn't on the list", s.Name)
			}
			if seen[s.Name] {
				t.Fatalf("%q twice", s.Name)
			}
			seen[s.Name] = true
			if strings.HasPrefix(s.Value, "--") || strings.IndexFunc(s.Value, func(c rune) bool { return !valueChar(c) || unicode.IsSpace(c) }) >= 0 {
				t.Fatalf("value %q", s.Value)
			}
		}
		// The command line it makes has each setting once, as an argument
		// of its own.
		in := Instance{Engine: engine, Limits: Limits{MemoryMiB: max(mem, MinMemoryMiB)}, Settings: Settings{Advanced: text}}
		if _, ok := advancedRules[engine]; ok {
			args := withAdvanced(engine, serviceSpec(in, "/d", "pw", "").Cmd, set)
			for _, a := range args {
				if strings.ContainsAny(a, "\n\r\x00") {
					t.Fatalf("argument %q", a)
				}
			}
		}
	})
}

func TestAdvancedOnTheCommandLine(t *testing.T) {
	spec := func(engine, adv string) string {
		in := Instance{ID: "x", Engine: engine, Limits: Limits{MemoryMiB: 1024}, Settings: Settings{Advanced: adv}}
		return strings.Join(serviceSpec(in, "/d", "pw", "").Cmd, " ")
	}
	for _, c := range []struct {
		engine, adv string
		want, not   []string
	}{
		{
			MariaDB, "innodb_buffer_pool_size = 256M\nmax_allowed_packet = 64M\nperformance_schema = on",
			[]string{"--innodb_buffer_pool_size=256M", "--max_allowed_packet=64M", "--performance_schema=ON", "--max-connections=100"},
			[]string{"--innodb-buffer-pool-size=512M", "--performance-schema=OFF"},
		},
		{MySQL, "slow_query_log = 1", []string{"--slow_query_log=ON", "--secure-file-priv=NULL"}, nil},
		{
			Postgres, "shared_buffers = 128MB\nwork_mem = 8MB",
			[]string{"-c shared_buffers=128MB", "-c work_mem=8MB", "-c effective_cache_size=768MB", "-c hba_file=/etc/raptor/pg_hba.conf"},
			[]string{"shared_buffers=256MB"},
		},
		{
			Redis, "maxmemory-policy = allkeys-lru\nhz = 20",
			[]string{"--maxmemory-policy allkeys-lru", "--hz 20", "--aclfile /data/users.acl"},
			[]string{"noeviction"},
		},
		// A line no longer allowed is left out, not a failed start.
		{Postgres, "work_mem = 8MB\nhba_file = /tmp/x", []string{"-c work_mem=8MB", "hba_file=/etc/raptor/pg_hba.conf"}, []string{"/tmp/x"}},
		{MongoDB, "cacheSizeGB = 1", nil, []string{"cacheSizeGB"}},
	} {
		got := spec(c.engine, c.adv)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q missing from %s", c.engine, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: %q in %s", c.engine, n, got)
			}
		}
	}
}

func TestSaveChecksAdvanced(t *testing.T) {
	in := InstanceInput{Name: "x", Engine: MariaDB, Limits: Limits{MemoryMiB: 512}, Settings: Settings{Advanced: " wait_timeout = 60\r\n\r\n"}}
	if err := in.validate(); err != nil || in.Settings.Advanced != "wait_timeout = 60" {
		t.Errorf("%q %v", in.Settings.Advanced, err)
	}
}

var update = flag.Bool("update", false, "write web/src/lib/db-advanced.json")

// The web app checks advanced settings with the same rules, from
// web/src/lib/db-advanced.json; go test -run TestAdvancedRulesForWeb
// -update writes it (then format it with Biome: pnpm --dir web lint:fix).
func TestAdvancedRulesForWeb(t *testing.T) {
	path := filepath.Join("..", "..", "..", "web", "src", "lib", "db-advanced.json")
	want := AdvancedRules()
	if *update {
		b, err := json.MarshalIndent(want, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil { //nolint:gosec // a source file
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path) //nolint:gosec // a source file
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]AdvancedEngine
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		for e := range want {
			for n, r := range want[e].Rules {
				if g, ok := got[e].Rules[n]; !ok || !reflect.DeepEqual(g, r) {
					t.Errorf("%s %s: web has %+v, Wings %+v", e, n, g, r)
				}
			}
		}
		t.Errorf("web/src/lib/db-advanced.json is out of date: go test ./internal/wings/databases -run TestAdvancedRulesForWeb -update")
	}
	if _, ok := want[MongoDB]; ok {
		t.Error("MongoDB has advanced settings: document them")
	}
}
