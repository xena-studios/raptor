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
func linkCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	token := fs.String("token", "", "join token from the Panel (rpt_join_…)")
	panelURL := fs.String("panel", "", "Panel URL (default: panel.url in the config)")
	cfgPath := fs.String("config", config.DefaultPath, "Wings config file")
	noRestart := fs.Bool("no-restart", false, "don't restart Wings afterwards")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *token == "" {
		return errors.New("usage: raptor link --token rpt_join_… (make a token in the Panel: Add Node)")
	}
	if os.Geteuid() != 0 {
		return errors.New("raptor link must run as root")
	}
	cfg, err := config.Load(*cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		cfg = config.Default()
	} else if err != nil {
		return err
	}
	if cfg.NodeID != "" {
		return fmt.Errorf("this node is already linked (as %s); to link it again, run raptor relink", cfg.NodeID)
	}
	if *panelURL == "" {
		*panelURL = cfg.Panel.URL
	}
	*panelURL = strings.TrimSuffix(*panelURL, "/")

	key, err := nodelink.LoadKey(cfg.Identity.Key)
	if err != nil {
		return err
	}
	if key == nil {
		if _, err := nodelink.GenerateKey(cfg.Identity.Key); err != nil {
			return fmt.Errorf("creating the node key: %w", err)
		}
		if key, err = nodelink.LoadKey(cfg.Identity.Key); err != nil {
			return err
		}
	}
	pub := key.Public().(ed25519.PublicKey)
	payload, err := nodelink.EnrollPayload(*token, pub)
	if err != nil {
		return err
	}
	name, _ := os.Hostname()
	ectx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	client := nodev1connect.NewEnrollmentServiceClient(&http.Client{Timeout: time.Minute}, *panelURL+"/api")
	res, err := client.Enroll(ectx, &nodev1.EnrollRequest{
		Token: *token, PublicKey: pub, Signature: ed25519.Sign(key, payload), Name: name,
		WingsVersion: buildinfo.Version, Facts: facts(),
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) && (ce.Code() == connect.CodePermissionDenied || ce.Code() == connect.CodeInvalidArgument) {
			return errors.New(ce.Message())
		}
		return fmt.Errorf("enrolling with %s: %w", *panelURL, err)
	}
	if len(res.GetPanelKey()) != ed25519.PublicKeySize {
		return errors.New("the Panel's answer has no valid signing key")
	}
	// The Panel's key first: a config with a node ID but no Panel key would
	// leave Wings unable to connect.
	if err := writeFileAtomic(cfg.Identity.PanelKey, []byte(base64.StdEncoding.EncodeToString(res.GetPanelKey())+"\n"), 0o644); err != nil {
		return fmt.Errorf("pinning the Panel's key: %w", err)
	}
	if err := config.SetLink(*cfgPath, res.GetNodeId(), *panelURL); err != nil {
		return fmt.Errorf("writing %s: %w", *cfgPath, err)
	}
	fmt.Printf("Linked as node %s (n-%s.raptornodes.net)\n", res.GetNodeId(), res.GetShortId())
	fmt.Printf("Panel key pinned: %s\n", base64.StdEncoding.EncodeToString(res.GetPanelKey()))
	if *noRestart {
		fmt.Println("Restart Wings to connect: systemctl restart raptor-wings")
		return nil
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", "raptor-wings").CombinedOutput(); err != nil {
		return fmt.Errorf("restarting Wings (systemctl restart raptor-wings): %w: %s", err, bytes.TrimSpace(out))
	}
	return waitConnected(ctx, cfg.Paths.Socket)
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
