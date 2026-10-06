package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// SetLink records the node's identity in the config file at path: node_id
// and panel.url (an empty node ID removes it, for unlinking). The rest of
// the file, comments included, is kept. The result must still be a valid
// config.
func SetLink(path, nodeID, panelURL string) error {
	return edit(path, func(root *yaml.Node) {
		if nodeID == "" {
			removeKey(root, "node_id")
		} else {
			setKey(root, "node_id", nodeID)
		}
		if panelURL != "" {
			setKey(child(root, "panel"), "url", panelURL)
		}
	})
}

// SetQuotas sets storage.quotas (off: soft limits) in the config file.
func SetQuotas(path string, on bool) error {
	return edit(path, func(root *yaml.Node) {
		s := child(root, "storage")
		setKey(s, "quotas", strconv.FormatBool(on))
		s.Content[find(s, "quotas")+1].Tag = "!!bool"
	})
}

// edit changes the config file at path, keeping the rest of it (comments
// included). The result must still be a valid config.
func edit(path string, change func(root *yaml.Node)) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-provided
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errors.New("config: not a mapping")
	}
	change(root)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if _, err := Parse(out.Bytes()); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(out.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func find(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func setKey(m *yaml.Node, key, value string) {
	if i := find(m, key); i >= 0 {
		m.Content[i+1].Kind, m.Content[i+1].Tag, m.Content[i+1].Value = yaml.ScalarNode, "!!str", value
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func removeKey(m *yaml.Node, key string) {
	if i := find(m, key); i >= 0 {
		m.Content = append(m.Content[:i], m.Content[i+2:]...)
	}
}

// child returns the mapping under key, creating it if needed.
func child(m *yaml.Node, key string) *yaml.Node {
	if i := find(m, key); i >= 0 && m.Content[i+1].Kind == yaml.MappingNode {
		return m.Content[i+1]
	} else if i >= 0 {
		m.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode}
		return m.Content[i+1]
	}
	c := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, c)
	return c
}
