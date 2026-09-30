package files

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Denylist is an egg's file_denylist: files users may see in listings but
// not read, change, move, or delete, through the web file manager or SFTP.
// Patterns use .gitignore syntax, as in Pterodactyl Wings:
//
//   - a pattern without a slash matches a name at any depth ("*.jar");
//     one with a slash is relative to the server's directory ("config/x.yml",
//     "/server.jar");
//   - "*" and "?" match within a name, "[...]" is a character class, and
//     "**" matches any number of directories ("**/secrets", "logs/**");
//   - a trailing slash matches only directories;
//   - "!" re-allows what an earlier pattern denied; later patterns win;
//   - everything inside a denied directory is denied, and can't be
//     re-allowed (as in git).
type Denylist struct {
	rules []rule
}

type rule struct {
	re      *regexp.Regexp
	literal string // instead of re, for patterns that aren't valid UTF-8
	anchor  bool
	negate  bool
	dirOnly bool
}

func (r rule) matches(name string) bool {
	if r.re != nil {
		return r.re.MatchString(name)
	}
	return name == r.literal || (!r.anchor && strings.HasSuffix(name, "/"+r.literal))
}

// Compile parses patterns. It never fails: a pattern that isn't a valid glob
// (an unclosed or reversed character class) matches its own text literally.
func Compile(patterns []string) *Denylist {
	d := &Denylist{}
	for _, p := range patterns {
		if r, ok := compileRule(p); ok {
			d.rules = append(d.rules, r)
		}
	}
	return d
}

// Empty reports whether nothing is denied.
func (d *Denylist) Empty() bool { return d == nil || len(d.rules) == 0 }

// Denied reports whether name (relative to the server's directory, as Rel
// returns it) is denied. isDir says whether name is a directory; its parents
// always are.
func (d *Denylist) Denied(name string, isDir bool) bool {
	if d.Empty() || name == "." || name == "" {
		return false
	}
	for i := range len(name) {
		if name[i] == '/' && d.match(name[:i], true) {
			return true
		}
	}
	return d.match(name, isDir)
}

func (d *Denylist) match(name string, isDir bool) bool {
	denied := false
	for _, r := range d.rules {
		if (!r.dirOnly || isDir) && r.matches(name) {
			denied = !r.negate
		}
	}
	return denied
}

func compileRule(p string) (rule, bool) {
	p = strings.TrimRight(p, " \t\r\n")
	if p == "" || strings.HasPrefix(p, "#") {
		return rule{}, false
	}
	var r rule
	if strings.HasPrefix(p, "!") {
		r.negate, p = true, p[1:]
	} else if strings.HasPrefix(p, `\!`) || strings.HasPrefix(p, `\#`) {
		p = p[1:]
	}
	if strings.HasSuffix(p, "/") {
		r.dirOnly, p = true, strings.TrimRight(p, "/")
	}
	anchored := strings.Contains(p, "/")
	p = strings.TrimLeft(p, "/")
	if p == "" {
		return rule{}, false
	}
	if !utf8.ValidString(p) {
		r.literal, r.anchor = path.Clean(p), anchored
		return r, true
	}
	var segs []string
	for s := range strings.SplitSeq(p, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	var b strings.Builder
	b.WriteString("^")
	if !anchored && segs[0] != "**" {
		b.WriteString("(?:.*/)?")
	}
	for i, s := range segs {
		last := i == len(segs)-1
		if s == "**" {
			switch {
			case len(segs) == 1 || last:
				b.WriteString(".*")
			default:
				b.WriteString("(?:.*/)?")
			}
			continue
		}
		b.WriteString(glob(s))
		if !last {
			b.WriteString("/")
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		prefix := "^"
		if !anchored {
			prefix = "^(?:.*/)?"
		}
		re = regexp.MustCompile(prefix + regexp.QuoteMeta(path.Clean(p)) + "$")
	}
	r.re = re
	return r, true
}

// glob translates one path segment of a glob to a regular expression.
func glob(seg string) string {
	s := []rune(seg)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '\\':
			if i+1 < len(s) {
				i++
				b.WriteString(regexp.QuoteMeta(string(s[i])))
			} else {
				b.WriteString(`\\`)
			}
		case '[':
			end := slices.Index(s[i+1:], ']')
			if end < 0 {
				b.WriteString(`\[`)
				continue
			}
			class := s[i+1 : i+1+end]
			i += end + 1
			b.WriteString("[")
			if len(class) > 0 && (class[0] == '!' || class[0] == '^') {
				b.WriteString("^/")
				class = class[1:]
			}
			for _, cc := range class {
				switch cc {
				case '\\', '[', ']', '^':
					b.WriteString(`\`)
				}
				b.WriteRune(cc)
			}
			b.WriteString("]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}
