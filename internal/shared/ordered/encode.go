package ordered

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// EncodeJSON writes a node tree as JSON, keeping key order. indent is the
// per-level indentation ("" writes compact JSON).
func EncodeJSON(n *yaml.Node, indent string) ([]byte, error) {
	var b bytes.Buffer
	if err := encodeJSON(&b, n, indent, 0); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func encodeJSON(b *bytes.Buffer, n *yaml.Node, indent string, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("nested too deeply")
	}
	nl := func(d int) {
		if indent != "" {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat(indent, d))
		}
	}
	sep := ":"
	if indent != "" {
		sep = ": "
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			b.WriteString("null")
			return nil
		}
		return encodeJSON(b, n.Content[0], indent, depth)
	case yaml.AliasNode:
		return encodeJSON(b, n.Alias, indent, depth+1)
	case yaml.MappingNode:
		if len(n.Content) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			writeString(b, n.Content[i].Value)
			b.WriteString(sep)
			if err := encodeJSON(b, n.Content[i+1], indent, depth+1); err != nil {
				return err
			}
		}
		nl(depth)
		b.WriteByte('}')
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteByte('[')
		for i, c := range n.Content {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			if err := encodeJSON(b, c, indent, depth+1); err != nil {
				return err
			}
		}
		nl(depth)
		b.WriteByte(']')
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!int", "!!float":
			if json.Valid([]byte(n.Value)) {
				b.WriteString(n.Value)
				return nil
			}
			writeString(b, n.Value)
		case "!!bool":
			if n.Value == "true" || n.Value == "false" {
				b.WriteString(n.Value)
				return nil
			}
			writeString(b, n.Value)
		case "!!null":
			b.WriteString("null")
		default:
			writeString(b, n.Value)
		}
	default:
		return fmt.Errorf("unsupported node kind %d", n.Kind)
	}
	return nil
}

// writeString writes a JSON string without HTML escaping: <, >, and & stay
// as they are, because config files aren't HTML.
func writeString(b *bytes.Buffer, s string) {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // strings always encode
	b.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))
}
