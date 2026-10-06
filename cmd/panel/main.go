// Command panel runs the Raptor Panel in one of its process roles.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/api"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

const usage = `usage: panel <command>

commands:
  serve api                 run the Panel (API, browser and node connections)
  migrate                   apply database migrations (PANEL_DATABASE_URL)
  keygen <path>             create the Panel's signing key (PANEL_SIGNING_KEY)
  org create <name>         add an org (until accounts exist)
  join-token <org-id> [name]  a single-use token that links one node (1 hour)
  version                   print version

environment:
  PANEL_DATABASE_URL   Postgres; without it, serve api has no node routes
  PANEL_SIGNING_KEY    the signing key file (nodes pin its public key)
  PANEL_API_ADDR       listen address (default :8080)`

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
	case len(args) == 1 && args[0] == "migrate":
		return migrate(ctx, log)
	case len(args) == 2 && args[0] == "serve" && args[1] == "api":
		return serveAPI(ctx, log)
	case len(args) == 2 && args[0] == "keygen":
		pub, err := nodelink.GenerateKey(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("signing key written to %s\npublic key: %s\n", args[1], base64.StdEncoding.EncodeToString(pub))
		return nil
	case len(args) == 3 && args[0] == "org" && args[1] == "create":
		return withRegistry(ctx, func(r *nodes.Registry) error {
			id, err := r.CreateOrg(ctx, args[2])
			if err == nil {
				fmt.Println(id)
			}
			return err
		})
	case (len(args) == 2 || len(args) == 3) && args[0] == "join-token":
		return withRegistry(ctx, func(r *nodes.Registry) error {
			name := ""
			if len(args) == 3 {
				name = args[2]
			}
			token, err := r.CreateJoinToken(ctx, args[1], name)
			if err == nil {
				fmt.Println(token)
			}
			return err
		})
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
		return nil
	}
}

// serveAPI runs the API. With a database it also enrolls nodes and holds
// their connections, which needs the signing key.
func serveAPI(ctx context.Context, log *slog.Logger) error {
	cfg := api.Config{Addr: envOr("PANEL_API_ADDR", ":8080")}
	if url := os.Getenv("PANEL_DATABASE_URL"); url != "" {
		pool, err := store.Open(ctx, url)
		if err != nil {
			return err
		}
		defer pool.Close()
		reg, err := registry(pool)
		if err != nil {
			return err
		}
		cfg.Nodes = reg
		router := &nodes.Router{DB: pool, ID: nodes.NewInstanceID(), Log: log}
		cfg.Hub = &nodes.Hub{
			PanelKey: reg.PanelKey, NodeKey: reg.NodeKey, Log: log,
			OnConnect: func(ctx context.Context, h nodelink.Hello) {
				router.Connected(ctx, h)
				if err := reg.Connected(ctx, h); err != nil {
					log.Error("recording a node connection", "node", h.NodeID, "err", err)
				}
			},
			OnDisconnect: func(ctx context.Context, id string) {
				router.Disconnected(ctx, id)
				if err := reg.Disconnected(ctx, id); err != nil {
					log.Error("recording a node disconnection", "node", id, "err", err)
				}
			},
		}
		router.Hub = cfg.Hub
		if err := router.Start(ctx); err != nil {
			return fmt.Errorf("panel instance: %w", err)
		}
		cfg.Router = router
		log.Info("panel instance", "id", router.ID)
		reg.KeyChanged = cfg.Hub.Disconnect
		mirror := &nodes.Mirror{DB: pool, Hub: cfg.Hub, Log: log}
		cfg.Hub.EventsAvailable = func(_ context.Context, id string, _ int64) { mirror.Notify(id) }

	} else {
		log.Warn("PANEL_DATABASE_URL is not set: nodes can't enroll or connect")
	}
	return api.Run(ctx, cfg, log)
}

func registry(pool *pgxpool.Pool) (*nodes.Registry, error) {
	path := os.Getenv("PANEL_SIGNING_KEY")
	if path == "" {
		return nil, errors.New("PANEL_SIGNING_KEY is not set (create one with panel keygen)")
	}
	key, err := nodelink.LoadKey(path)
	if err != nil {
		return nil, err
	}
	if key == nil {
		return nil, fmt.Errorf("%s doesn't exist (create it with panel keygen)", path)
	}
	return &nodes.Registry{DB: pool, PanelKey: key}, nil
}

func withRegistry(ctx context.Context, fn func(*nodes.Registry) error) error {
	url := os.Getenv("PANEL_DATABASE_URL")
	if url == "" {
		return errors.New("PANEL_DATABASE_URL is not set")
	}
	pool, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Tokens and orgs don't need the signing key.
	return fn(&nodes.Registry{DB: pool})
}

func migrate(ctx context.Context, log *slog.Logger) error {
	url := os.Getenv("PANEL_DATABASE_URL")
	if url == "" {
		return errors.New("PANEL_DATABASE_URL is not set")
	}
	pool, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := store.Migrate(ctx, pool); err != nil {
		return err
	}
	log.Info("migrations applied")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
