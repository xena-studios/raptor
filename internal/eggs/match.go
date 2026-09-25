package eggs

import (
	"regexp"
	"strings"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// Matcher matches a console line against a "done" string. Like Pterodactyl, a
// value prefixed with "regex:" is a regular expression; anything else matches
// as a substring.
type Matcher struct {
	raw string
	re  *regexp.Regexp
}

// NewMatcher builds a matcher. An invalid regex falls back to substring matching
// of the whole value, as Pterodactyl does.
func NewMatcher(s string) Matcher {
	if p, ok := strings.CutPrefix(s, "regex:"); ok && p != "" {
		if re, err := regexp.Compile(p); err == nil {
			return Matcher{raw: s, re: re}
		}
	}
	return Matcher{raw: s}
}

// Match reports whether line matches.
func (m Matcher) Match(line string) bool {
	if m.re != nil {
		return m.re.MatchString(line)
	}
	return strings.Contains(line, m.raw)
}

func (m Matcher) String() string { return m.raw }

// IsDone reports whether a console line marks the server as started. Eggs
// with no done strings are considered started as soon as they launch.
func (c Config) IsDone(line string) bool {
	if c.StripANSI {
		line = ansi.ReplaceAllString(line, "")
	}
	for _, m := range c.Done {
		if m.Match(line) {
			return true
		}
	}
	return false
}
