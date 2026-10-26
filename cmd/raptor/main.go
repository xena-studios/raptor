// Command raptor is the Wings daemon and the local CLI.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/wings"
	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/localapi"
	"github.com/xena-studios/raptor/internal/wings/update"
)

// errUsage is an unknown command; the usage has been printed.
var errUsage = errors.New("unknown command")

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		if !errors.Is(err, errDoctorFailed) {
			fmt.Fprintln(os.Stderr, "raptor:", err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case len(args) >= 1 && args[0] == "version":
		fmt.Println(buildinfo.String())
		return nil
	case len(args) >= 1 && args[0] == "status":
		return status(ctx, args[1:])
	case len(args) >= 1 && args[0] == "ps":
		return ps(ctx, args[1:])
	case len(args) >= 1 && (args[0] == "start" || args[0] == "stop" || args[0] == "restart" || args[0] == "kill"):
		return power(ctx, args[0], args[1:])
	case len(args) >= 1 && args[0] == "console":
		return console(ctx, args[1:])
	case len(args) >= 1 && args[0] == "logs":
		return logs(ctx, args[1:])
	case len(args) >= 1 && args[0] == "backup":
		return backupCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "update":
		return updateCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "storage":
		return storageCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "doctor":
		return doctorCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "keys":
		return keysCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "support":
		return supportCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "audit":
		return auditCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "import":
		return importCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "tui":
		return tuiCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "bootstrap":
		return bootstrapCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "link":
		return linkCmd(ctx, args[1:], false)
	case len(args) >= 1 && args[0] == "relink":
		return linkCmd(ctx, args[1:], true)
	case len(args) >= 1 && args[0] == "unlink":
		return unlinkCmd(ctx, args[1:])
	case len(args) >= 1 && args[0] == "uninstall":
		return uninstallCmd(ctx, args[1:])
	case len(args) >= 2 && args[0] == "wings" && args[1] == "run":
		return wingsRun(ctx, args[2:])
	case len(args) >= 2 && args[0] == "wings" && args[1] == "backup-worker":
		// Started by Wings for each backup operation; not for people.
		backup.PrepareWorker()
		return backup.Serve(ctx, os.Stdin, os.Stdout)
	case len(args) >= 2 && args[0] == "wings" && args[1] == "shutdown-servers":
		return shutdownServers(ctx, args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		return errUsage
	}
}

func wingsRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("wings run", flag.ContinueOnError)
	path := fs.String("config", config.DefaultPath, "config file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s not found: this node hasn't been set up yet", *path)
	}
	if err != nil {
		return err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.Log.Level)); err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	// An update waiting for its trial runs as this process's child; this
	// version takes over again if it fails.
	l := &update.Launcher{Layout: update.Layout{Dir: update.DefaultDir}, StatePath: update.StatePath(cfg.Paths.State), Args: os.Args[1:], Log: log}
	if code, handled := l.Run(); handled {
		os.Exit(code)
	}
	return wings.Run(ctx, cfg, log)
}

func status(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	socket := fs.String("socket", config.Default().Paths.Socket, "Wings socket")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	s, err := localapi.Dial(*socket).GetStatus(ctx, &localv1.GetStatusRequest{})
	if err != nil {
		return fmt.Errorf("can't reach Wings at %s (is raptor-wings running, and are you root or in the raptor group?): %w", *socket, err)
	}
	printStatus(s)
	return nil
}

func printStatus(s *localv1.GetStatusResponse) {
	linked := "not linked"
	if s.GetNodeId() != "" {
		linked = "linked as " + s.GetNodeId() + " to " + s.GetPanelUrl()
	}
	docker := "✗ unreachable: " + s.GetDocker().GetError()
	if s.GetDocker().GetReachable() {
		docker = "✓ " + s.GetDocker().GetVersion()
	}
	uptime := time.Since(s.GetStartedAt().AsTime()).Round(time.Second)
	fmt.Printf("Wings    %s (%s), up %s\n", s.GetVersion(), s.GetCommit(), uptime)
	fmt.Printf("Panel    %s\n", linked)
	if c := s.GetConnection(); c != nil {
		since := time.Since(c.GetSince().AsTime()).Round(time.Second)
		switch c.GetState() {
		case "connected":
			ping := fmt.Sprintf("ping %d ms", c.GetRttMs())
			switch {
			case c.GetRttMs() > 0:
			case since < 31*time.Second: // the first ping is 30 s in
				ping = "no ping yet"
			default:
				ping = "ping <1 ms"
			}
			fmt.Printf("         ✓ connected for %s (%s, %d reconnects)\n", since, ping, c.GetReconnects())
		default:
			msg := "✗ " + c.GetState() + " for " + since.String()
			if e := c.GetLastError(); e != "" {
				msg += ": " + e
			}
			fmt.Printf("         %s\n", msg)
		}
	}
	fmt.Printf("Docker   %s\n", docker)
	if st := s.GetStorage(); st != nil {
		limits := "quotas"
		if !st.GetQuotas() {
			limits = "soft limits"
		}
		if st.GetReady() {
			fmt.Printf("Storage  ✓ %s (%s)\n", st.GetPath(), limits)
		} else {
			fmt.Printf("Storage  ✗ %s (see raptor storage status)\n", st.GetError())
		}
	}
	if hd := s.GetHostDisk(); hd != nil && len(hd.GetDisks()) > 0 {
		lowest := slices.MinFunc(hd.GetDisks(), func(a, b *localv1.DiskSpace) int { return cmp.Compare(a.GetFree(), b.GetFree()) })
		if hd.GetLow() {
			fmt.Printf("Disk     ✗ %s free on %s, below %s: installs and image pulls are refused\n", host.Bytes(lowest.GetFree()), lowest.GetPath(), host.Bytes(hd.GetMinFree()))
		} else {
			fmt.Printf("Disk     ✓ %s free (least on %s)\n", host.Bytes(lowest.GetFree()), lowest.GetPath())
		}
	}
	if u := s.GetUpdate(); u != nil {
		switch u.GetStatus() {
		case "trial":
			fmt.Printf("Update   trying %s (from %s)\n", u.GetTo(), u.GetFrom())
		case "succeeded":
			fmt.Printf("Update   ✓ %s → %s, %s\n", u.GetFrom(), u.GetTo(), u.GetFinishedAt().AsTime().Local().Format(time.DateTime))
		case "failed":
			fmt.Printf("Update   ✗ %s → %s rolled back, %s: %s\n", u.GetFrom(), u.GetTo(), u.GetFinishedAt().AsTime().Local().Format(time.DateTime), u.GetError())
		default:
			fmt.Printf("Update   %s\n", u.GetError())
		}
	}
	if n := s.GetServers(); n != nil {
		fmt.Printf("Servers  %d, %d up\n", n.GetTotal(), n.GetUp())
	} else {
		fmt.Println("Servers  - (the container runtime isn't ready; see raptor-wings logs)")
	}
}

func shutdownServers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("wings shutdown-servers", flag.ContinueOnError)
	socket := fs.String("socket", config.Default().Paths.Socket, "Wings socket")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	res, err := localapi.Dial(*socket).ShutdownServers(ctx, &localv1.ShutdownServersRequest{})
	if err != nil {
		return fmt.Errorf("can't stop servers through Wings at %s: %w", *socket, err)
	}
	fmt.Printf("stopped %d server(s)\n", res.GetStopped())
	for _, e := range res.GetErrors() {
		fmt.Fprintln(os.Stderr, "raptor:", e)
	}
	if len(res.GetErrors()) > 0 {
		return errors.New("some servers didn't stop cleanly")
	}
	return nil
}
