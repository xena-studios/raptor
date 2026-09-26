// Package ordered decodes and encodes JSON and YAML as yaml.Node trees, so
// key order, comments (YAML), and number formatting survive a round trip.
package ordered

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Decode parses JSON or YAML into a yaml.Node, preserving key order. JSON is
// decoded with encoding/json because some valid JSON (e.g. the "\/" escape) is
// not valid YAML.
func Decode(data []byte) (*yaml.Node, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return DecodeJSON(trimmed)
	}
	return DecodeYAML(data)
}

// DecodeJSON parses a JSON document. Numbers keep their original text.
func DecodeJSON(data []byte) (*yaml.Node, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	n, err := jsonValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after JSON value")
	}
	return n, nil
}

// DecodeYAML parses a YAML document and returns its root node (not the
// document node).
func DecodeYAML(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, errors.New("empty document")
	}
	return doc.Content[0], nil
}

// maxDepth bounds nesting so hostile input can't exhaust the stack.
const maxDepth = 512

func jsonValue(dec *json.Decoder, depth int) (*yaml.Node, error) {
	if depth > maxDepth {
		return nil, errors.New("JSON nested too deeply")
	}
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
				val, err := jsonValue(dec, depth+1)
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
				val, err := jsonValue(dec, depth+1)
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
		tag := "!!int"
		if strings.ContainsAny(v.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: v.String()}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}
