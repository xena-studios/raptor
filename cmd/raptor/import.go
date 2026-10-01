package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/ptero"
)

func importCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "pterodactyl" {
		return errors.New("usage: raptor import pterodactyl -key <application API key> [-server <id>…] [-dry-run] [-start]")
	}
	var key, panelURL, cfgPath *string
	var dry, start, yes, noSuspend *bool
	var only multiFlag
	c, _, err := dial("import pterodactyl", args[1:], func(fs *flag.FlagSet) {
		key = fs.String("key", os.Getenv("RAPTOR_PTERODACTYL_KEY"), "the Pterodactyl Panel's application API key (ptla_…; or $RAPTOR_PTERODACTYL_KEY)")
		panelURL = fs.String("panel", "", "the Pterodactyl Panel's URL (default: from Pterodactyl Wings' config)")
		cfgPath = fs.String("pterodactyl-config", ptero.DefaultConfig, "Pterodactyl Wings' config file")
		fs.Var(&only, "server", "import only this server (UUID, short ID, or name; repeatable)")
		dry = fs.Bool("dry-run", false, "show what would be imported, and change nothing")
		start = fs.Bool("start", false, "start each server in Raptor after importing it")
		yes = fs.Bool("yes", false, "don't ask for confirmation")
		noSuspend = fs.Bool("no-suspend", false, "don't suspend imported servers in Pterodactyl")
	})
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("must be run as root")
	}
	if *key == "" {
		return errors.New("-key is required: create an application API key in the Pterodactyl Panel (Admin → Application API) with read access to servers, nodes, nests, eggs, and allocations, and write access to servers (for suspending)")
	}
	wc, err := ptero.ReadWingsConfig(*cfgPath)
	if err != nil {
		return fmt.Errorf("pterodactyl wings: %w", err)
	}
	pc := &ptero.Client{PanelURL: *panelURL, Key: *key, Wings: wc}
	if pc.PanelURL == "" {
		pc.PanelURL = wc.Remote
	}
	node, err := pc.NodeID(ctx)
	if err != nil {
		return err
	}
	all, err := pc.Servers(ctx, node)
	if err != nil {
		return err
	}
	var servers []ptero.Server
	for _, s := range all {
		if len(only) == 0 || slices.ContainsFunc(only, func(o string) bool { return o == s.UUID || o == s.Identifier || o == s.Name }) {
			servers = append(servers, s)
		}
	}
	if len(servers) == 0 {
		fmt.Println("No servers to import on this node.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tPTERODACTYL ID\tMEMORY\tDISK\tPORTS\tFILES")
	for _, s := range servers {
		var ports []string
		for _, a := range s.Allocations {
			ports = append(ports, fmt.Sprintf("%s:%d", a.IP, a.Port))
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Identifier, mib(s.Limits.Memory), mib(s.Limits.Disk), strings.Join(ports, " "), pc.Dir(s.UUID))
	}
	_ = w.Flush()
	if *dry {
		return nil
	}
	fmt.Println("\nEach server is stopped in Pterodactyl, its files are copied into Raptor (the originals stay where they are),")
	if !*noSuspend {
		fmt.Println("and it's suspended in Pterodactyl so it isn't started there again by mistake (unsuspend it there to undo).")
	}
	if !*yes {
		if !isTerminal(os.Stdin) {
			return errors.New("run it in a terminal to confirm, or pass -yes")
		}
		fmt.Print("Type import to continue: ")
		if line, _ := bufio.NewReader(os.Stdin).ReadString('\n'); strings.TrimSpace(line) != "import" {
			return errors.New("nothing imported")
		}
	}

	eggs := map[[2]int][]byte{}
	failed := 0
	for _, s := range servers {
		if err := importOne(ctx, c, pc, s, eggs, *start, !*noSuspend, os.Stdout); err != nil {
			failed++
			fmt.Printf("✗ %s: %v\n", s.Name, err)
		}
	}
	fmt.Println("\nPterodactyl's config and files weren't changed. Once everything runs in Raptor, stop Pterodactyl's Wings:")
	fmt.Println("    systemctl disable --now wings")
	if failed > 0 {
		return fmt.Errorf("%d of %d servers weren't imported", failed, len(servers))
	}
	return nil
}

func importOne(ctx context.Context, c localApi, pc *ptero.Client, s ptero.Server, eggs map[[2]int][]byte, start, suspend bool, out io.Writer) error {
	k := [2]int{s.Nest, s.EggID}
	egg, ok := eggs[k]
	if !ok {
		var err error
		if egg, err = pc.Egg(ctx, s.Nest, s.EggID); err != nil {
			return fmt.Errorf("egg: %w", err)
		}
		eggs[k] = egg
	}
	_, _ = fmt.Fprintf(out, "%s: stopping in Pterodactyl…\n", s.Name)
	if err := pc.Stop(ctx, s.UUID, 2*time.Minute); err != nil {
		return fmt.Errorf("stop in Pterodactyl: %w", err)
	}
	cfg := s.Config(egg)
	l := cfg.Limits
	req := &localv1.ImportServerRequest{
		Name: cfg.Name, Egg: cfg.Egg, EggSource: cfg.EggSource, Image: cfg.Image, Startup: cfg.Startup,
		Variables: cfg.Variables, SourceDir: pc.Dir(s.UUID),
		Limits: &localv1.ImportLimits{MemoryMib: l.MemoryMiB, SwapMib: l.SwapMiB, DiskMib: l.DiskMiB, CpuPercent: l.CPUPercent, Cpuset: l.Cpuset},
	}
	for _, a := range cfg.Allocations {
		req.Allocations = append(req.Allocations, &localv1.ImportAllocation{Ip: a.IP, Port: int32(a.Port), Primary: a.Primary}) //nolint:gosec // a port
	}
	_, _ = fmt.Fprintf(out, "%s: copying files…\n", s.Name)
	res, err := c.ImportServer(ctx, req)
	if err != nil {
		return rpcErr(err)
	}
	_, _ = fmt.Fprintf(out, "✓ %s imported as %s (%d files, %s)\n", s.Name, short(res.GetServerId()), res.GetFiles(), host.Bytes(res.GetBytes()))
	if suspend {
		if err := pc.Suspend(ctx, s.ID); err != nil {
			_, _ = fmt.Fprintf(out, "  ! couldn't suspend it in Pterodactyl: %v; don't start it there\n", err)
		}
	}
	if start {
		if _, err := c.Power(ctx, &localv1.PowerRequest{Server: res.GetServerId(), Action: localv1.PowerAction_POWER_ACTION_START}); err != nil {
			return fmt.Errorf("imported, but didn't start: %w", rpcErr(err))
		}
		_, _ = fmt.Fprintf(out, "  started\n")
	}
	return nil
}

// localApi is the part of the local API client importing uses.
type localApi interface {
	ImportServer(ctx context.Context, req *localv1.ImportServerRequest) (*localv1.ImportServerResponse, error)
	Power(ctx context.Context, req *localv1.PowerRequest) (*localv1.PowerResponse, error)
}

func mib(n int64) string {
	if n == 0 {
		return "unlimited"
	}
	return host.Bytes(n << 20)
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
