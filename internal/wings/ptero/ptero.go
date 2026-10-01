// Package ptero reads servers from a Pterodactyl install on the same box,
// for `raptor import pterodactyl` (docs/WINGS.md#pterodactyl-import): their
// eggs, variables, limits, and allocations from the Pterodactyl Panel's
// application API, and their files from Pterodactyl Wings' data directory.
// It never changes anything in Pterodactyl but stopping the servers it moves.
package ptero

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// DefaultConfig is Pterodactyl Wings' config file.
const DefaultConfig = "/etc/pterodactyl/config.yml"

// WingsConfig is what's needed from Pterodactyl Wings' config.
type WingsConfig struct {
	UUID   string `yaml:"uuid"` // the node
	Token  string `yaml:"token"`
	Remote string `yaml:"remote"` // the Panel's URL
	API    struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
		SSL  struct {
			Enabled bool `yaml:"enabled"`
		} `yaml:"ssl"`
	} `yaml:"api"`
	System struct {
		Data string `yaml:"data"` // server directories: <data>/<uuid>
	} `yaml:"system"`
}

// ReadWingsConfig reads Pterodactyl Wings' config file.
func ReadWingsConfig(path string) (WingsConfig, error) {
	var c WingsConfig
	b, err := os.ReadFile(path) //nolint:gosec // an operator-given path
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.System.Data == "" {
		c.System.Data = "/var/lib/pterodactyl/volumes"
	}
	if c.API.Port == 0 {
		c.API.Port = 8080
	}
	if c.UUID == "" || c.Token == "" {
		return c, fmt.Errorf("%s: no node uuid or token: is this node configured?", path)
	}
	return c, nil
}

// Client calls the Pterodactyl Panel's application API (a ptla_ key) and
// this node's Pterodactyl Wings.
type Client struct {
	PanelURL string
	Key      string // application API key
	Wings    WingsConfig
	HTTP     *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) do(ctx context.Context, method, rawURL, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %s: %s", method, rawURL, res.Status, strings.TrimSpace(string(b[:min(len(b), 300)])))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func (c *Client) panel(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, strings.TrimRight(c.PanelURL, "/")+path, c.Key, nil, out)
}

// Server is a Pterodactyl server on this node, with what Raptor needs.
type Server struct {
	ID          int
	UUID        string
	Identifier  string
	Name        string
	Suspended   bool
	Nest, EggID int
	Image       string
	Startup     string
	Variables   map[string]string
	Limits      Limits
	Allocations []Allocation
}

// Limits are a Pterodactyl server's limits (MiB and percent; 0 = none).
type Limits struct {
	Memory, Swap, Disk, CPU int64
	Threads                 string
}

// Allocation is an IP and port.
type Allocation struct {
	IP      string
	Port    int
	Primary bool
}

type list[T any] struct {
	Data []struct {
		Attributes T `json:"attributes"`
	} `json:"data"`
	Meta struct {
		Pagination struct {
			CurrentPage int `json:"current_page"`
			TotalPages  int `json:"total_pages"`
		} `json:"pagination"`
	} `json:"meta"`
}

// NodeID returns the Panel's ID for this node (by its UUID in Wings'
// config).
func (c *Client) NodeID(ctx context.Context) (int, error) {
	for page := 1; ; page++ {
		var l list[struct {
			ID   int    `json:"id"`
			UUID string `json:"uuid"`
		}]
		if err := c.panel(ctx, fmt.Sprintf("/api/application/nodes?per_page=100&page=%d", page), &l); err != nil {
			return 0, err
		}
		for _, n := range l.Data {
			if n.Attributes.UUID == c.Wings.UUID {
				return n.Attributes.ID, nil
			}
		}
		if page >= l.Meta.Pagination.TotalPages {
			return 0, fmt.Errorf("the Panel at %s has no node with this box's UUID %s", c.PanelURL, c.Wings.UUID)
		}
	}
}

type serverAttrs struct {
	ID         int    `json:"id"`
	UUID       string `json:"uuid"`
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Suspended  bool   `json:"suspended"`
	Node       int    `json:"node"`
	Allocation int    `json:"allocation"`
	Nest       int    `json:"nest"`
	Egg        int    `json:"egg"`
	Limits     struct {
		Memory  int64  `json:"memory"`
		Swap    int64  `json:"swap"`
		Disk    int64  `json:"disk"`
		CPU     int64  `json:"cpu"`
		Threads string `json:"threads"`
	} `json:"limits"`
	Container struct {
		StartupCommand string `json:"startup_command"`
		Image          string `json:"image"`
	} `json:"container"`
	Relationships struct {
		Allocations list[struct {
			ID   int    `json:"id"`
			IP   string `json:"ip"`
			Port int    `json:"port"`
		}] `json:"allocations"`
		Variables list[struct {
			Env   string  `json:"env_variable"`
			Value *string `json:"server_value"`
		}] `json:"variables"`
	} `json:"relationships"`
}

// Servers returns the servers on a node.
func (c *Client) Servers(ctx context.Context, node int) ([]Server, error) {
	var out []Server
	for page := 1; ; page++ {
		var l list[serverAttrs]
		if err := c.panel(ctx, fmt.Sprintf("/api/application/servers?include=allocations,variables&per_page=100&page=%d", page), &l); err != nil {
			return nil, err
		}
		for _, d := range l.Data {
			a := d.Attributes
			if a.Node != node {
				continue
			}
			s := Server{
				ID: a.ID, UUID: a.UUID, Identifier: a.Identifier, Name: a.Name, Suspended: a.Suspended,
				Nest: a.Nest, EggID: a.Egg, Image: a.Container.Image, Startup: a.Container.StartupCommand,
				Variables: map[string]string{},
				Limits:    Limits{Memory: a.Limits.Memory, Swap: a.Limits.Swap, Disk: a.Limits.Disk, CPU: a.Limits.CPU, Threads: a.Limits.Threads},
			}
			for _, v := range a.Relationships.Variables.Data {
				if v.Attributes.Value != nil {
					s.Variables[v.Attributes.Env] = *v.Attributes.Value
				}
			}
			for _, al := range a.Relationships.Allocations.Data {
				s.Allocations = append(s.Allocations, Allocation{IP: al.Attributes.IP, Port: al.Attributes.Port, Primary: al.Attributes.ID == a.Allocation})
			}
			out = append(out, s)
		}
		if page >= l.Meta.Pagination.TotalPages {
			return out, nil
		}
	}
}

type eggAttrs struct {
	ID           int               `json:"id"`
	Name         string            `json:"name"`
	Author       string            `json:"author"`
	Description  string            `json:"description"`
	DockerImages map[string]string `json:"docker_images"`
	Startup      string            `json:"startup"`
	Config       struct {
		Files        json.RawMessage `json:"files"`
		Startup      json.RawMessage `json:"startup"`
		Stop         *string         `json:"stop"`
		Logs         json.RawMessage `json:"logs"`
		FileDenylist []string        `json:"file_denylist"`
		Extends      *int            `json:"extends"`
	} `json:"config"`
	Script struct {
		Install   *string `json:"install"`
		Entry     *string `json:"entry"`
		Container *string `json:"container"`
		Extends   *int    `json:"extends"`
	} `json:"script"`
	Relationships struct {
		Variables list[struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			Env          string `json:"env_variable"`
			Default      string `json:"default_value"`
			UserViewable bool   `json:"user_viewable"`
			UserEditable bool   `json:"user_editable"`
			Rules        string `json:"rules"`
		}] `json:"variables"`
	} `json:"relationships"`
}

func (c *Client) egg(ctx context.Context, nest, id int) (eggAttrs, error) {
	var e struct {
		Attributes eggAttrs `json:"attributes"`
	}
	err := c.panel(ctx, fmt.Sprintf("/api/application/nests/%d/eggs/%d?include=variables", nest, id), &e)
	return e.Attributes, err
}

func empty(r json.RawMessage) bool {
	s := strings.TrimSpace(string(r))
	return s == "" || s == "null"
}

// Egg rebuilds a server's egg as a PTDL_v2 file, the format Raptor reads,
// taking inherited settings from the eggs it extends.
func (c *Client) Egg(ctx context.Context, nest, id int) ([]byte, error) {
	e, err := c.egg(ctx, nest, id)
	if err != nil {
		return nil, err
	}
	cfg, script := e, e
	for range 5 { // eggs extend eggs in the same nest
		if cfg.Config.Extends == nil || !empty(cfg.Config.Files) || !empty(cfg.Config.Startup) {
			break
		}
		if cfg, err = c.egg(ctx, nest, *cfg.Config.Extends); err != nil {
			return nil, fmt.Errorf("egg %d's parent: %w", id, err)
		}
	}
	for range 5 {
		if script.Script.Extends == nil || (script.Script.Install != nil && *script.Script.Install != "") {
			break
		}
		if script, err = c.egg(ctx, nest, *script.Script.Extends); err != nil {
			return nil, fmt.Errorf("egg %d's install script: %w", id, err)
		}
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	jsonText := func(r json.RawMessage, dflt string) string {
		if empty(r) {
			return dflt
		}
		// The API returns these decoded; eggs carry them as JSON text.
		var s string
		if json.Unmarshal(r, &s) == nil {
			return s
		}
		return string(r)
	}
	vars := []map[string]any{}
	for _, v := range e.Relationships.Variables.Data {
		a := v.Attributes
		vars = append(vars, map[string]any{
			"name": a.Name, "description": a.Description, "env_variable": a.Env, "default_value": a.Default,
			"user_viewable": a.UserViewable, "user_editable": a.UserEditable, "rules": a.Rules, "field_type": "text",
		})
	}
	denylist := e.Config.FileDenylist
	if denylist == nil {
		denylist = []string{}
	}
	return json.MarshalIndent(map[string]any{
		"_comment":      "Rebuilt from the Pterodactyl Panel by raptor import pterodactyl",
		"meta":          map[string]any{"version": "PTDL_v2", "update_url": nil},
		"exported_at":   time.Now().UTC().Format(time.RFC3339),
		"name":          e.Name,
		"author":        e.Author,
		"description":   e.Description,
		"docker_images": e.DockerImages,
		"file_denylist": denylist,
		"startup":       e.Startup,
		"config": map[string]any{
			"files":   jsonText(cfg.Config.Files, "{}"),
			"startup": jsonText(cfg.Config.Startup, "{}"),
			"logs":    jsonText(cfg.Config.Logs, "{}"),
			"stop":    str(cfg.Config.Stop),
		},
		"scripts": map[string]any{"installation": map[string]any{
			"script": str(script.Script.Install), "container": str(script.Script.Container), "entrypoint": str(script.Script.Entry),
		}},
		"variables": vars,
	}, "", "    ")
}

// wings calls this node's Pterodactyl Wings.
func (c *Client) wings(ctx context.Context, method, path string, body, out any) error {
	host := c.Wings.API.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if c.Wings.API.SSL.Enabled {
		scheme = "https"
	}
	u := url.URL{Scheme: scheme, Host: host + ":" + strconv.Itoa(c.Wings.API.Port), Path: path}
	return c.do(ctx, method, u.String(), c.Wings.Token, body, out)
}

// State returns a server's state in Pterodactyl Wings (offline, starting,
// running, stopping).
func (c *Client) State(ctx context.Context, uuid string) (string, error) {
	var s struct {
		State string `json:"state"`
	}
	err := c.wings(ctx, http.MethodGet, "/api/servers/"+uuid, nil, &s)
	return s.State, err
}

// Stop stops a server in Pterodactyl Wings and waits for it, killing it if
// it hasn't stopped within timeout.
func (c *Client) Stop(ctx context.Context, uuid string, timeout time.Duration) error {
	if st, err := c.State(ctx, uuid); err != nil {
		return err
	} else if st == "offline" {
		return nil
	}
	if err := c.wings(ctx, http.MethodPost, "/api/servers/"+uuid+"/power", map[string]any{"action": "stop"}, nil); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	killed := false
	for {
		st, err := c.State(ctx, uuid)
		if err != nil {
			return err
		}
		if st == "offline" {
			return nil
		}
		if time.Now().After(deadline) {
			if killed {
				return errors.New("the server didn't stop, even after a kill")
			}
			if err := c.wings(ctx, http.MethodPost, "/api/servers/"+uuid+"/power", map[string]any{"action": "kill"}, nil); err != nil {
				return err
			}
			killed, deadline = true, time.Now().Add(30*time.Second)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Dir returns a server's directory in Pterodactyl's data directory.
func (c *Client) Dir(uuid string) string { return filepath.Join(c.Wings.System.Data, uuid) }

// Suspend suspends a server in the Pterodactyl Panel, so it isn't started
// there again by mistake after moving to Raptor (reversible: unsuspend it
// in Pterodactyl).
func (c *Client) Suspend(ctx context.Context, id int) error {
	return c.do(ctx, http.MethodPost, fmt.Sprintf("%s/api/application/servers/%d/suspend", strings.TrimRight(c.PanelURL, "/"), id), c.Key, nil, nil)
}

// Config is the server as Raptor creates it, with its rebuilt egg.
func (s Server) Config(egg []byte) server.Config {
	cfg := server.Config{
		Name: s.Name, Egg: egg, EggSource: fmt.Sprintf("pterodactyl:%d/%d", s.Nest, s.EggID),
		Image: s.Image, Startup: s.Startup, Variables: s.Variables, Settings: server.DefaultSettings(),
		Limits: containers.Limits{
			MemoryMiB: s.Limits.Memory, SwapMiB: max(s.Limits.Swap, 0), DiskMiB: s.Limits.Disk,
			CPUPercent: s.Limits.CPU, Cpuset: s.Limits.Threads,
		},
	}
	for _, a := range s.Allocations {
		cfg.Allocations = append(cfg.Allocations, server.Allocation{IP: a.IP, Port: a.Port, Primary: a.Primary})
	}
	return cfg
}
