package configfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/eggs"
)

var parsers = []string{"properties", "file", "ini", "json", "yaml", "xml"}

func FuzzEdit(f *testing.F) {
	seeds := map[int]string{
		0: "a=1\nb:2\nc 3\nlong=x\\\n  y\n#c\n",
		1: "server.port 1\r\nx\n",
		2: "k=v\n[s]\nk = \"q\"\n[/Script/E.G]\nM=1\n",
		3: `{"a":{"b":[1,{"c":true}]},"d":null}`,
		4: bungee,
		5: spaceEngineers,
	}
	for i, s := range seeds {
		f.Add(i, []byte(s), "a.b", "", "v")
		f.Add(i, []byte(s), "servers.*.address", `regex:^(127\.0\.0\.1)(:\d+)?$`, "x$2")
		f.Add(i, []byte(s), "list[0].host", "", "0.0.0.0:1")
	}
	f.Fuzz(func(t *testing.T, p int, data []byte, key, ifValue, value string) {
		if len(data) > 1<<16 {
			return
		}
		parser := parsers[(p%len(parsers)+len(parsers))%len(parsers)]
		out, err := Edit(parser, data, []Rule{{Key: key, IfValue: ifValue, Value: value}})
		if err != nil {
			return
		}
		// Whatever we write must be readable again by the same parser.
		if _, err := Edit(parser, out, nil); err != nil {
			t.Fatalf("%s output doesn't parse again: %v\ninput: %q\noutput: %q", parser, err, data, out)
		}
	})
}

// Any key and value written to a .properties file read back exactly.
func FuzzPropertiesRoundTrip(f *testing.F) {
	f.Add("motd", "Héllo \\ world")
	f.Add("a key", " leading\ttab\nnewline 🦖")
	f.Add("#=:!", "=:")
	f.Fuzz(func(t *testing.T, key, value string) {
		if key == "" || !utf8Valid(key) || !utf8Valid(value) {
			return
		}
		out := editProperties(nil, []Rule{{Key: key, Value: value}})
		entries := propertyEntries(strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"))
		if len(entries) != 1 || entries[0].key != key || entries[0].value != value {
			t.Fatalf("wrote %q=%q as %q, read back %+v", key, value, out, entries)
		}
	})
}

// No egg path can make Apply touch anything outside the server directory.
func FuzzApplyPath(f *testing.F) {
	for _, p := range []string{"server.properties", "../outside.txt", "/etc/passwd", "a/../../b", "..", "sub/../../../x", `..\..\x`, "link/x"} {
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, p string) {
		base := t.TempDir()
		dir := filepath.Join(base, "server")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(base, "outside.txt")
		if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		// A symlink pointing out of the server directory.
		_ = os.Symlink(base, filepath.Join(dir, "link"))

		root, err := os.OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = root.Close() }()
		_ = Apply(root, []eggs.ConfigFile{{Path: p, Parser: "properties", Find: []eggs.FindRule{{Key: "k", Value: "v"}}}},
			eggs.Values{}, Owner{UID: os.Getuid(), GID: os.Getgid()})

		if b, _ := os.ReadFile(sentinel); string(b) != "keep" {
			t.Fatalf("path %q changed a file outside the root", p)
		}
		_ = filepath.WalkDir(base, func(q string, d fs.DirEntry, err error) error {
			if err == nil && q != base && q != sentinel && !strings.HasPrefix(q, dir+string(filepath.Separator)) && q != dir {
				t.Fatalf("path %q created %s outside the root", p, q)
			}
			return nil
		})
	})
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }
