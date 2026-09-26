package configfile

import (
	"strings"
)

// editINI edits an INI file line by line. A rule key names "section.key":
// the first dot outside brackets splits the section from the key, brackets
// are dropped, and later dots belong to the key, exactly as Pterodactyl reads
// them ("[/Script/Engine.GameSession].MaxPlayers", "SystemSettings.net.Foo").
// A key without a dot is in the section-less part at the top of the file.
//
// Every matching key in the section is rewritten in place (keeping its
// spacing and quotes). Missing keys are added at the end of the section, and
// missing sections at the end of the file. Pterodactyl's INI library
// rewrites the whole file and collapses duplicate keys; this doesn't.
func editINI(data []byte, rules []Rule) []byte {
	nl := lineEnding(data)
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	trailing := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}

	for _, r := range rules {
		section, key := iniPath(r.Key)
		lines = setINI(lines, section, key, r)
	}

	s := strings.Join(lines, nl)
	if trailing || len(lines) > 0 {
		s += nl
	}
	return []byte(s)
}

// iniPath splits a rule key into section and key the way Pterodactyl does.
func iniPath(match string) (section, key string) {
	var path []string
	var cur strings.Builder
	depth := 0
	for _, c := range match {
		switch c {
		case '[':
			depth++
		case ']':
			depth--
		case '.':
			if depth > 0 || len(path) == 1 {
				cur.WriteRune(c)
				continue
			}
			path = append(path, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	path = append(path, cur.String())
	if len(path) == 2 {
		return path[0], path[1]
	}
	return "", path[0]
}

type iniLine struct {
	section string
	key     string
	valAt   int // offset of the value in the line
	isKey   bool
	header  bool
}

func parseINILines(lines []string) []iniLine {
	out := make([]iniLine, len(lines))
	section := ""
	for i, l := range lines {
		t := strings.TrimSpace(strings.TrimPrefix(l, "\uFEFF"))
		switch {
		case t == "" || t[0] == ';' || t[0] == '#':
		case t[0] == '[' && strings.HasSuffix(t, "]"):
			section = strings.TrimSpace(t[1 : len(t)-1])
			out[i].header = true
		default:
			sep := strings.IndexAny(l, "=:")
			if sep < 0 {
				break
			}
			at := sep + 1
			for at < len(l) && (l[at] == ' ' || l[at] == '\t') {
				at++
			}
			out[i].key = strings.TrimSpace(strings.TrimPrefix(l[:sep], "\uFEFF"))
			out[i].valAt = at
			out[i].isKey = true
		}
		out[i].section = section
	}
	return out
}

func setINI(lines []string, section, key string, r Rule) []string {
	parsed := parseINILines(lines)
	found := false
	for i, p := range parsed {
		if !p.isKey || p.section != section || p.key != key {
			continue
		}
		found = true
		old := lines[i][p.valAt:]
		val := strings.TrimSpace(old)
		quote := ""
		if quoted(val) {
			quote = val[:1]
			val = val[1 : len(val)-1]
		}
		ok, replaced := r.match(val)
		if !ok {
			continue
		}
		v := r.String()
		if strings.HasPrefix(r.IfValue, "regex:") {
			v = replaced
		}
		if quoted(v) {
			quote = "" // the egg's value brings its own quotes
		}
		lines[i] = lines[i][:p.valAt] + quote + v + quote
	}
	if found || r.IfValue != "" {
		return lines
	}

	// Add the key: after the last line of the section's last key (or its
	// header), with the separator style the file already uses.
	sep := "="
	for i, p := range parsed {
		if p.isKey {
			// The separator with its surrounding spaces: "=", " = ", …
			l := lines[i]
			keyEnd := len(strings.TrimRight(l[:strings.IndexAny(l, "=:")], " \t"))
			sep = l[keyEnd:p.valAt]
			break
		}
	}
	newLine := key + sep + r.String()

	insert := -1
	for i, p := range parsed {
		if p.section != section {
			continue
		}
		if p.isKey || (p.header && section != "") {
			insert = i + 1
		}
	}
	if insert < 0 && section == "" {
		// No top-level keys yet: before the first section header.
		insert = 0
		for i, p := range parsed {
			if p.header {
				insert = i
				break
			}
			insert = i + 1
		}
		return append(lines[:insert], append([]string{newLine}, lines[insert:]...)...)
	}
	if insert < 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		return append(lines, "["+section+"]", newLine)
	}
	return append(lines[:insert], append([]string{newLine}, lines[insert:]...)...)
}

func quoted(s string) bool {
	return len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0]
}
