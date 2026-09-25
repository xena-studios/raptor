// Command panel runs the Raptor Panel in one of its process roles.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/xena-studios/raptor/internal/panel/api"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
)

const usage = `usage: panel <command>

commands:
  serve api      run the API role
  serve tunnel   run the tunnel role
  version        print version`

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
	case len(args) == 2 && args[0] == "serve" && args[1] == "api":
		return api.Run(ctx, api.Config{Addr: envOr("PANEL_API_ADDR", ":8080")}, log)
	case len(args) == 2 && args[0] == "serve" && args[1] == "tunnel":
		return errors.New("tunnel role not implemented yet")
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
		return nil
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
