package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/localapi"
)

// linkCmd enrolls the node with a join token (docs/ARCHITECTURE.md#enrollment):
// it creates the node key if there's none, trades the token for a node ID,
// pins the Panel's signing key, records both in config.yml, and restarts
// Wings, which then connects. Servers keep running through the restart.
//
// With relink, the node keeps its ID and hostname (its key was revoked, or
// it was removed in the Panel, or it was unlinked): it always gets a new key,
// and the old one is replaced only once the Panel has the new one.
func linkCmd(ctx context.Context, args []string, relink bool) error {
	name := "link"
	if relink {
		name = "relink"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	token := fs.String("token", "", "join token from the Panel (rpt_join_…)")
	panelURL := fs.String("panel", "", "Panel URL (default: panel.url in the config)")
	cfgPath := fs.String("config", config.DefaultPath, "Wings config file")
	noRestart := fs.Bool("no-restart", false, "don't restart Wings afterwards")
	var nodeID *string
	if relink {
		nodeID = fs.String("node", "", "the node's ID (default: node_id in the config)")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *token == "" {
		return fmt.Errorf("usage: raptor %s -token rpt_join_… (make a token in the Panel: Add Node)", name)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("raptor %s must run as root", name)
	}
	cfg, err := config.Load(*cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		cfg = config.Default()
	} else if err != nil {
		return err
	}
	switch {
	case !relink && cfg.NodeID != "":
		return fmt.Errorf("this node is already linked (as %s); to link it again, run raptor relink", cfg.NodeID)
	case relink && *nodeID == "":
		*nodeID = cfg.NodeID
		if *nodeID == "" {
			return errors.New("this node isn't linked: give its ID with -node, or link it as a new node (raptor link)")
		}
	}
	if *panelURL == "" {
		*panelURL = cfg.Panel.URL
	}
	*panelURL = strings.TrimSuffix(*panelURL, "/")

	// The key: the existing one when linking, a new one when re-linking
	// (written beside the old one until the Panel has it).
	keyPath := cfg.Identity.Key
	if relink {
		keyPath += ".new"
		_ = os.Remove(keyPath)
	}
	key, err := nodelink.LoadKey(keyPath)
	if err != nil {
		return err
	}
	if key == nil {
		if _, err := nodelink.GenerateKey(keyPath); err != nil {
			return fmt.Errorf("creating the node key: %w", err)
		}
		if key, err = nodelink.LoadKey(keyPath); err != nil {
			return err
		}
	}
	pub := key.Public().(ed25519.PublicKey)
	payload, err := nodelink.EnrollPayload(*token, pub)
	if err != nil {
		return err
	}
	req := &nodev1.EnrollRequest{
		Token: *token, PublicKey: pub, Signature: ed25519.Sign(key, payload),
		WingsVersion: buildinfo.Version, Facts: facts(),
	}
	req.Name, _ = os.Hostname()
	if relink {
		req.NodeId = *nodeID
	}
	ectx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	client := nodev1connect.NewEnrollmentServiceClient(&http.Client{Timeout: time.Minute}, *panelURL+"/api")
	res, err := client.Enroll(ectx, req)
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) && ce.Code() != connect.CodeUnavailable && ce.Code() != connect.CodeUnknown {
			return errors.New(ce.Message())
		}
		return fmt.Errorf("enrolling with %s: %w", *panelURL, err)
	}
	if len(res.GetPanelKey()) != ed25519.PublicKeySize {
		return errors.New("the Panel's answer has no valid signing key")
	}
	if relink {
		if err := os.Rename(keyPath, cfg.Identity.Key); err != nil {
			return fmt.Errorf("installing the new node key: %w", err)
		}
	}
	// The Panel's key first: a config with a node ID but no Panel key would
	// leave Wings unable to connect.
	if err := writeFileAtomic(cfg.Identity.PanelKey, []byte(base64.StdEncoding.EncodeToString(res.GetPanelKey())+"\n"), 0o644); err != nil {
		return fmt.Errorf("pinning the Panel's key: %w", err)
	}
	if err := config.SetLink(*cfgPath, res.GetNodeId(), *panelURL); err != nil {
		return fmt.Errorf("writing %s: %w", *cfgPath, err)
	}
	verb := "Linked"
	if relink {
		verb = "Re-linked"
	}
	fmt.Printf("%s as node %s (n-%s.raptornodes.net)\n", verb, res.GetNodeId(), res.GetShortId())
	fmt.Printf("Panel key pinned: %s\n", base64.StdEncoding.EncodeToString(res.GetPanelKey()))
	if *noRestart {
		fmt.Println("Restart Wings to connect: systemctl restart raptor-wings")
		return nil
	}
	if err := restartWings(ctx); err != nil {
		return err
	}
	return waitConnected(ctx, cfg.Paths.Socket)
}

// unlinkCmd stops the node connecting to the Panel: it forgets its node ID,
// its key, and the Panel's key, and restarts Wings. Servers keep running and are
// managed from the box; remote commands are refused. The node stays in the
// Panel (shown offline) and can come back with raptor relink.
func unlinkCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unlink", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultPath, "Wings config file")
	yes := fs.Bool("yes", false, "don't ask")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("raptor unlink must run as root")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if cfg.NodeID == "" {
		return errors.New("this node isn't linked")
	}
	if !*yes {
		fmt.Printf("Unlink node %s from %s? Servers keep running; the Panel can't reach them until you relink. [y/N] ", cfg.NodeID, cfg.Panel.URL)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			return errors.New("not unlinked")
		}
	}
	if err := config.SetLink(*cfgPath, "", ""); err != nil {
		return err
	}
	// The node key goes too: relinking makes a new one, and a later link as
	// a new node shouldn't reuse the old node's identity.
	for _, p := range []string{cfg.Identity.PanelKey, cfg.Identity.Key} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	fmt.Printf("Unlinked (was node %s). To link it again: raptor relink -node %s -token …\n", cfg.NodeID, cfg.NodeID)
	return restartWings(ctx)
}

func restartWings(ctx context.Context) error {
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", "raptor-wings").CombinedOutput(); err != nil {
		return fmt.Errorf("restarting Wings (systemctl restart raptor-wings): %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// waitConnected waits for Wings to report the node connection up.
func waitConnected(ctx context.Context, socket string) error {
	deadline := time.Now().Add(time.Minute)
	last := ""
	for time.Now().Before(deadline) {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		s, err := localapi.Dial(socket).GetStatus(sctx, &localv1.GetStatusRequest{})
		cancel()
		if err == nil && s.GetConnection() != nil {
			c := s.GetConnection()
			if c.GetState() == "connected" {
				fmt.Println("Connected to the Panel.")
				return nil
			}
			last = c.GetLastError()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if last != "" {
		return fmt.Errorf("linked, but Wings hasn't connected yet: %s (see raptor status)", last)
	}
	return errors.New("linked, but Wings hasn't connected yet (see raptor status and journalctl -u raptor-wings)")
}

func facts() *nodev1.NodeFacts {
	f := &nodev1.NodeFacts{Arch: runtime.GOARCH, Cpus: int32(min(runtime.NumCPU(), 1<<16))} //nolint:gosec // bounded
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		f.Kernel = strings.TrimSpace(string(b))
	}
	if m, err := host.MemTotal("/proc/meminfo"); err == nil {
		f.MemoryBytes = m
	}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
				f.Os = strings.Trim(v, `"`)
			}
		}
	}
	return f
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
