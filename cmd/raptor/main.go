// Command raptor is the Wings daemon and the local CLI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/wings"
	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/localapi"
)

const usage = `usage: raptor <command> [flags]

commands:
  status      show node status (talks to the running daemon)
  ps          list servers with their state and resource usage
  start|stop|restart|kill <server>
              power actions (root); stop waits for a clean shutdown
  console <server>
              live console; type commands to send them (root)
  logs <server> [-n lines] [-f] [-t]
              server output, further back than the console history
  backup list|create|restore
              list backups, back up now, or restore one (root for create
              and restore)
  storage status|setup|grow
              show, create, or enlarge the server data volume (root for
              setup and grow)
  wings run   run the Wings daemon
  wings shutdown-servers
              gracefully stop every server for a host shutdown (root; used
              by raptor-shutdown.service, servers start again at boot)
  version     print version

<server> is a server's ID, the short ID from ps, or its exact name.
Run "raptor <command> -h" for a command's flags.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "raptor:", err)
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
	case len(args) >= 1 && args[0] == "storage":
		return storageCmd(ctx, args[1:])
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
		os.Exit(2)
		return nil
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
