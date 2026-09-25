// Command raptor is the Wings daemon and the local CLI.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/wings"
)

const usage = `usage: raptor <command>

commands:
  wings run   run the Wings daemon
  version     print version`

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(os.Args[1:], log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(args []string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case len(args) == 1 && args[0] == "version":
		fmt.Println(buildinfo.String())
		return nil
	case len(args) == 2 && args[0] == "wings" && args[1] == "run":
		return wings.Run(ctx, log)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
		return nil
	}
}
