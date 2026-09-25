package eggs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// decode parses JSON or YAML into a yaml.Node, preserving key order. JSON is
// decoded with encoding/json because some valid JSON (e.g. the "\/" escape) is
// not valid YAML.
func decode(data []byte) (*yaml.Node, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		n, err := jsonValue(dec)
		if err != nil {
			return nil, err
		}
		if dec.More() {
			return nil, errors.New("trailing data after JSON value")
		}
		return n, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, errors.New("empty document")
	}
	return doc.Content[0], nil
}

func jsonValue(dec *json.Decoder) (*yaml.Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("unexpected key %v", kt)
				}
				val, err := jsonValue(dec)
				if err != nil {
					return nil, err
				}
				n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
			}
			_, err := dec.Token() // '}'
			return n, err
		case '[':
			n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for dec.More() {
				val, err := jsonValue(dec)
				if err != nil {
					return nil, err
				}
				n.Content = append(n.Content, val)
			}
			_, err := dec.Token() // ']'
			return n, err
		}
		return nil, fmt.Errorf("unexpected %v", v)
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}, nil
	case json.Number:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: v.String()}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}
