package files

import (
	"strings"
	"testing"
)

func TestDenylist(t *testing.T) {
	tests := []struct {
		patterns []string
		name     string
		dir      bool
		want     bool
	}{
		// A name without a slash matches at any depth.
		{[]string{"server.jar"}, "server.jar", false, true},
		{[]string{"server.jar"}, "old/server.jar", false, true},
		{[]string{"server.jar"}, "server.jar.bak", false, false},
		{[]string{"*.jar"}, "plugins/x.jar", false, true},
		{[]string{"*.jar"}, "x.jar/y", false, true}, // inside a denied directory
		{[]string{"*.jar"}, "plugins/x.jarx", false, false},
		// A slash anchors it to the server's directory.
		{[]string{"/server.jar"}, "server.jar", false, true},
		{[]string{"/server.jar"}, "old/server.jar", false, false},
		{[]string{"config/secret.yml"}, "config/secret.yml", false, true},
		{[]string{"config/secret.yml"}, "x/config/secret.yml", false, false},
		// Directories and everything in them.
		{[]string{"secrets/"}, "secrets", true, true},
		{[]string{"secrets/"}, "secrets", false, false}, // a file named like it
		{[]string{"secrets/"}, "secrets/a/b", false, true},
		{[]string{"secrets"}, "secrets/a", false, true},
		// ** spans directories.
		{[]string{"**/cache"}, "cache", true, true},
		{[]string{"**/cache"}, "a/b/cache", true, true},
		{[]string{"logs/**"}, "logs/a/b.log", false, true},
		{[]string{"logs/**"}, "logs", true, false},
		{[]string{"a/**/b"}, "a/b", false, true},
		{[]string{"a/**/b"}, "a/x/y/b", false, true},
		{[]string{"**"}, "anything/at/all", false, true},
		// * and ? stay within a name.
		{[]string{"/*.txt"}, "a/b.txt", false, false},
		{[]string{"file?.txt"}, "file1.txt", false, true},
		{[]string{"file?.txt"}, "file10.txt", false, false},
		// Character classes.
		{[]string{"log[0-9].txt"}, "log5.txt", false, true},
		{[]string{"log[!0-9].txt"}, "log5.txt", false, false},
		{[]string{"log[!0-9].txt"}, "logx.txt", false, true},
		// Later patterns win; "!" re-allows, except inside a denied
		// directory.
		{[]string{"*.yml", "!keep.yml"}, "keep.yml", false, false},
		{[]string{"*.yml", "!keep.yml"}, "other.yml", false, true},
		{[]string{"!keep.yml", "*.yml"}, "keep.yml", false, true},
		{[]string{"conf/", "!conf/keep.yml"}, "conf/keep.yml", false, true},
		// Comments, blanks, escapes, and invalid globs.
		{[]string{"# server.jar", "", "  "}, "# server.jar", false, false},
		{[]string{`\#notes`}, "#notes", false, true},
		{[]string{`\!important`}, "!important", false, true},
		{[]string{`a\*b`}, "a*b", false, true},
		{[]string{`a\*b`}, "axb", false, false},
		{[]string{"bad[z-a]"}, "bad[z-a]", false, true}, // literal
		{[]string{"unclosed[abc"}, "unclosed[abc", false, true},
		{[]string{"dir/unclosed["}, "dir/unclosed[", false, true},
		// Names with regexp metacharacters and non-ASCII.
		{[]string{"a+b(c).txt"}, "a+b(c).txt", false, true},
		{[]string{"a+b(c).txt"}, "aab(c).txt", false, false},
		{[]string{"ünïcode*.yml"}, "x/ünïcode-1.yml", false, true},
		// The directory itself is never denied.
		{[]string{"**"}, ".", true, false},
	}
	for _, tt := range tests {
		got := Compile(tt.patterns).Denied(tt.name, tt.dir)
		if got != tt.want {
			t.Errorf("%q: Denied(%q, dir=%v) = %v, want %v", tt.patterns, tt.name, tt.dir, got, tt.want)
		}
	}
	if !Compile(nil).Empty() || !Compile([]string{"", "# x"}).Empty() || (*Denylist)(nil).Denied("x", false) {
		t.Error("an empty denylist denies something")
	}
}

func FuzzDenylist(f *testing.F) {
	f.Add("*.jar\n!keep.jar\nsecrets/", "plugins/keep.jar", false)
	f.Add("a/**/b\n[!x]?", "a/q/b", true)
	f.Add(`\[x\]`, "[x]", false)
	f.Fuzz(func(t *testing.T, patterns, name string, dir bool) {
		d := Compile(strings.Split(patterns, "\n"))
		name = Rel(name)
		got := d.Denied(name, dir)
		// Everything under a denied directory is denied.
		if got && dir && d.Denied(name+"/x", false) != true {
			t.Fatalf("%q denied as a directory, but not what's in it", name)
		}
	})
}
