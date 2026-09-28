//go:build e2e

package conformance

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

// configValues flattens a config file into path → value, so files written by
// different editors (Pterodactyl re-serializes; Raptor edits in place) can be
// compared by what they say rather than how they're laid out.
func configValues(parser string, data []byte) (map[string]string, error) {
	out := map[string]string{}
	switch parser {
	case "properties":
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l == "" || l[0] == '#' || l[0] == '!' {
				continue
			}
			i := strings.IndexAny(l, "=:")
			if i < 0 {
				out[l] = ""
				continue
			}
			out[strings.TrimSpace(l[:i])] = javaUnescape(strings.TrimSpace(l[i+1:]))
		}
	case "json", "yaml", "yml":
		// YAML is a superset of JSON; both decode the same way.
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		flatten("", v, out)
	case "ini":
		section := ""
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			switch {
			case l == "" || l[0] == ';' || l[0] == '#':
			case l[0] == '[' && strings.HasSuffix(l, "]"):
				section = l[1 : len(l)-1]
			default:
				k, v, _ := strings.Cut(l, "=")
				out[section+"."+strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	case "xml":
		dec := xml.NewDecoder(bytes.NewReader(data))
		var path []string
		seen := map[string]int{}
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			switch x := tok.(type) {
			case xml.StartElement:
				p := strings.Join(append(path, x.Name.Local), "/")
				n := seen[p]
				seen[p]++
				el := fmt.Sprintf("%s[%d]", x.Name.Local, n)
				path = append(path, el)
				for _, a := range x.Attr {
					out[strings.Join(path, "/")+"@"+a.Name.Local] = a.Value
				}
			case xml.EndElement:
				path = path[:len(path)-1]
			case xml.CharData:
				if s := strings.TrimSpace(string(x)); s != "" && len(path) > 0 {
					out[strings.Join(path, "/")] = s
				}
			}
		}
	default: // "file": line by line
		for i, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			out[fmt.Sprintf("line %03d", i+1)] = strings.TrimRight(l, "\r")
		}
	}
	return out, nil
}

func flatten(prefix string, v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			flatten(join(prefix, k), x[k], out)
		}
	case []any:
		for i, e := range x {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), e, out)
		}
	default:
		out[prefix] = fmt.Sprintf("%v (%T)", x, x)
	}
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

func sysStat(fi os.FileInfo) (*syscall.Stat_t, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return st, ok
}

// javaUnescape reads a .properties value the way Java does: \uXXXX, \t,
// \n, \r, \f, and a backslash before anything else is dropped (so \" is ").
func javaUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch c := s[i]; c {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'f':
			b.WriteByte('\f')
		case 'u':
			if i+4 < len(s) {
				if r, err := strconv.ParseUint(s[i+1:i+5], 16, 32); err == nil {
					b.WriteRune(rune(r))
					i += 4
					continue
				}
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
