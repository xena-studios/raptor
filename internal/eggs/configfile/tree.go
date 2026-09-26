package configfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/xena-studios/raptor/internal/shared/ordered"
)

// JSON and YAML files are edited as yaml.Node trees, so key order survives
// (and YAML comments), unlike Pterodactyl's convert-to-JSON-and-back.
//
// Rule keys are dotted paths:
//
//	a.b.c            nested keys; missing ones are created
//	list[0].host     array index (also "list.0.host")
//	servers.*.addr   every child of "servers"
//
// Pterodactyl mishandles "list[0].host" (it creates a key literally named
// "list[0]") and only supports one "*"; both work as intended here.

func editJSON(data []byte, rules []Rule) ([]byte, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if len(bytes.TrimSpace(data)) > 0 {
		var err error
		if root, err = ordered.DecodeJSON(bytes.TrimSpace(data)); err != nil {
			return nil, err
		}
	}
	for _, r := range rules {
		apply(root, splitPath(r.Key), r, 0)
	}
	return ordered.EncodeJSON(root, jsonIndent(data))
}

// jsonIndent returns the file's indentation: the leading whitespace of its
// first indented line, "" for single-line JSON, and 4 spaces for new files.
func jsonIndent(data []byte) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "    "
	}
	if !bytes.Contains(trimmed, []byte("\n")) {
		return ""
	}
	for _, line := range strings.Split(string(trimmed), "\n")[1:] {
		ws := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if ws != "" {
			return ws
		}
	}
	return "    "
}

func editYAML(data []byte, rules []Rule) ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	if len(bytes.TrimSpace(data)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		var first yaml.Node
		if err := dec.Decode(&first); err != nil {
			return nil, err
		}
		var next yaml.Node
		if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
			return nil, errors.New("files with more than one YAML document aren't supported")
		}
		if len(first.Content) > 0 {
			doc = &first
			if doc.Content[0].Kind == yaml.ScalarNode && doc.Content[0].Tag == "!!null" {
				doc.Content[0] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
		}
	}
	for _, r := range rules {
		apply(doc.Content[0], splitPath(r.Key), r, 0)
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(yamlIndent(data))
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// yamlIndent guesses the file's indentation from its first indented line
// (a key or a list item); 2 if there's none.
func yamlIndent(data []byte) int {
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimLeft(line, " ")
		if n := len(line) - len(t); n > 0 && t != "" && t[0] != '#' {
			if n > 8 {
				return 2
			}
			return n
		}
	}
	return 2
}

type segment struct {
	key     string // "" when the segment is only an index
	wild    bool
	indexes []int
}

var indexSuffix = regexp.MustCompile(`\[(\d+)\]`)

func splitPath(p string) []segment {
	var out []segment
	for _, part := range strings.Split(p, ".") {
		s := segment{wild: part == "*"}
		key := part
		if i := strings.IndexByte(part, '['); i >= 0 && strings.HasSuffix(part, "]") {
			rest := part[i:]
			if indexSuffix.ReplaceAllString(rest, "") == "" {
				key = part[:i]
				for _, m := range indexSuffix.FindAllStringSubmatch(rest, -1) {
					n, err := strconv.Atoi(m[1])
					if err != nil {
						break
					}
					s.indexes = append(s.indexes, n)
				}
			}
		}
		s.key = key
		out = append(out, s)
	}
	return out
}

// apply walks the path from n and applies the rule at the end. Missing
// containers are created only for unconditional rules, as in Pterodactyl.
func apply(n *yaml.Node, path []segment, r Rule, depth int) {
	if depth > 256 {
		return
	}
	if n.Kind == yaml.AliasNode {
		return // never write through an alias into shared data
	}
	if len(path) == 0 {
		setNode(n, r)
		return
	}
	create := r.IfValue == ""
	seg, rest := path[0], path[1:]

	if seg.wild {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 1; i < len(n.Content); i += 2 {
				apply(n.Content[i], rest, r, depth+1)
			}
		case yaml.SequenceNode:
			for _, c := range n.Content {
				apply(c, rest, r, depth+1)
			}
		}
		return
	}

	child := n
	if seg.key != "" {
		child = step(n, seg.key, create, len(rest) > 0 || len(seg.indexes) > 0)
	}
	for _, idx := range seg.indexes {
		if child == nil {
			return
		}
		child = index(child, idx, create)
	}
	if child != nil {
		apply(child, rest, r, depth+1)
	}
}

// step returns the child for key: a mapping key, or an index into a
// sequence when the key is a number. It creates a missing mapping key (as
// an empty mapping if the path continues) when create is set.
func step(n *yaml.Node, key string, create, container bool) *yaml.Node {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
	case yaml.SequenceNode:
		if i, err := strconv.Atoi(key); err == nil {
			return index(n, i, create)
		}
		return nil
	case yaml.ScalarNode:
		if n.Tag != "!!null" || !create {
			return nil
		}
		*n = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	default:
		return nil
	}
	if !create {
		return nil
	}
	val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
	if container {
		val = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
	return val
}

// index returns element i. A missing element can only be created by
// appending (i == length), and an empty placeholder becomes a sequence.
func index(n *yaml.Node, i int, create bool) *yaml.Node {
	if create && (n.Kind == yaml.MappingNode && len(n.Content) == 0 || n.Kind == yaml.ScalarNode && n.Tag == "!!null") {
		*n = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	if n.Kind != yaml.SequenceNode {
		return nil
	}
	if i < len(n.Content) {
		return n.Content[i]
	}
	if !create || i != len(n.Content) {
		return nil
	}
	val := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	n.Content = append(n.Content, val)
	return val
}

// setNode writes the rule's value into n, if its condition holds.
func setNode(n *yaml.Node, r Rule) {
	var v any
	if r.IfValue == "" {
		v = r.typed()
	} else {
		if n.Kind != yaml.ScalarNode {
			return
		}
		ok, replaced := r.match(n.Value)
		if !ok {
			return
		}
		v = r.typed()
		if strings.HasPrefix(r.IfValue, "regex:") {
			v = replaced // regex replacements are always strings
		}
	}

	style := n.Style
	*n = yaml.Node{Kind: yaml.ScalarNode, Anchor: n.Anchor, HeadComment: n.HeadComment, LineComment: n.LineComment, FootComment: n.FootComment}
	switch x := v.(type) {
	case bool:
		n.Tag, n.Value = "!!bool", strconv.FormatBool(x)
	case int:
		n.Tag, n.Value = "!!int", strconv.Itoa(x)
	case int64:
		n.Tag, n.Value = "!!int", strconv.FormatInt(x, 10)
	case uint64:
		n.Tag, n.Value = "!!int", strconv.FormatUint(x, 10)
	case float64:
		n.Tag, n.Value = "!!float", strconv.FormatFloat(x, 'f', -1, 64)
	default:
		n.Tag, n.Value = "!!str", fmt.Sprint(x)
		if style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
			n.Style = style
		}
	}
}
