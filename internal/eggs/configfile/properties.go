package configfile

import (
	"fmt"
	"strings"
	"unicode/utf16"
)

// editProperties edits a Java .properties file (Minecraft's server.properties
// and friends), following java.util.Properties' syntax: "=", ":", or
// whitespace separate keys from values, "#"/"!" start comments, a trailing
// backslash continues a line, and \uXXXX escapes non-ASCII characters (Java
// reads these files as ISO-8859-1).
//
// Every entry with a rule's key is rewritten in place, keeping the key's
// original spelling and separator. Keys that aren't in the file are appended.
func editProperties(data []byte, rules []Rule) []byte {
	nl := lineEnding(data)
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	trailing := len(lines) > 0 && lines[len(lines)-1] == ""
	if trailing {
		lines = lines[:len(lines)-1]
	}

	entries := propertyEntries(lines)
	deleted := map[int]bool{} // continuation lines of replaced values
	for _, r := range rules {
		found := false
		for i := range entries {
			e := &entries[i]
			if e.key != r.Key {
				continue
			}
			found = true
			ok, replaced := r.match(e.value)
			if !ok {
				continue
			}
			v := r.String()
			if strings.HasPrefix(r.IfValue, "regex:") {
				v = replaced
			}
			e.value = v
			lines[e.first] = e.prefix + escapeProperty(v, false)
			for j := e.first + 1; j <= e.last; j++ {
				deleted[j] = true
			}
			e.last = e.first
		}
		if !found && r.IfValue == "" {
			line := escapeProperty(r.Key, true) + "=" + escapeProperty(r.String(), false)
			entries = append(entries, propertyEntry{key: r.Key, value: r.String(), first: len(lines), last: len(lines), prefix: escapeProperty(r.Key, true) + "="})
			lines = append(lines, line)
		}
	}
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		if !deleted[i] {
			out = append(out, l)
		}
	}
	s := strings.Join(out, nl)
	if trailing || len(out) > 0 {
		s += nl
	}
	return []byte(s)
}

type propertyEntry struct {
	key, value  string
	first, last int    // physical lines the entry spans
	prefix      string // the first line up to where the value starts
}

func propertyEntries(lines []string) []propertyEntry {
	var out []propertyEntry
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t\f")
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == '!' {
			continue
		}
		first := i
		logical := trimmed
		for continues(logical) && i+1 < len(lines) {
			i++
			logical = logical[:len(logical)-1] + strings.TrimLeft(lines[i], " \t\f")
		}
		if continues(logical) {
			logical = logical[:len(logical)-1]
		}
		key, valStart := splitProperty(logical)
		// Where the value starts on the first physical line (continuation
		// lines only ever belong to the value when the key fits on one line).
		lead := len(lines[first]) - len(trimmed)
		prefix := lines[first]
		if lead+valStart <= len(lines[first]) {
			prefix = lines[first][:lead+valStart]
		}
		out = append(out, propertyEntry{
			key:    unescapeProperty(key),
			value:  unescapeProperty(logical[valStart:]),
			first:  first,
			last:   i,
			prefix: prefix,
		})
	}
	return out
}

// continues reports whether a line ends in an odd number of backslashes.
func continues(s string) bool {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// splitProperty returns the raw key and the offset where the value starts.
func splitProperty(s string) (key string, valStart int) {
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			i += 2
			continue
		}
		if c == '=' || c == ':' || c == ' ' || c == '\t' || c == '\f' {
			break
		}
		i++
	}
	if i > len(s) {
		i = len(s)
	}
	key = s[:i]
	j := i
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\f') {
		j++
	}
	if j < len(s) && (s[j] == '=' || s[j] == ':') {
		j++
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\f') {
			j++
		}
	}
	return key, j
}

func unescapeProperty(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	var pending []uint16 // UTF-16 code units from \u escapes (surrogate pairs)
	flush := func() {
		if len(pending) > 0 {
			b.WriteString(string(utf16.Decode(pending)))
			pending = pending[:0]
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			flush()
			b.WriteByte(c)
			continue
		}
		i++
		switch s[i] {
		case 't':
			flush()
			b.WriteByte('\t')
		case 'n':
			flush()
			b.WriteByte('\n')
		case 'r':
			flush()
			b.WriteByte('\r')
		case 'f':
			flush()
			b.WriteByte('\f')
		case 'u':
			var r uint16
			if i+4 < len(s) {
				if _, err := fmt.Sscanf(s[i+1:i+5], "%04x", &r); err == nil {
					pending = append(pending, r)
					i += 4
					continue
				}
			}
			flush()
			b.WriteByte('u')
		default:
			flush()
			b.WriteByte(s[i])
		}
	}
	flush()
	return b.String()
}

// escapeProperty escapes a key or value for a .properties file.
func escapeProperty(s string, key bool) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == ' ' && (key || i == 0):
			b.WriteString(`\ `)
		case key && (r == '=' || r == ':' || ((r == '#' || r == '!') && i == 0)):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r > 0x7e:
			for _, u := range utf16.Encode([]rune{r}) {
				fmt.Fprintf(&b, `\u%04X`, u)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
