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
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/api"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/dns"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/rollout"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

const usage = `usage: panel <command>

commands:
  serve api                 run the Panel (API, browser and node connections)
  migrate                   apply database migrations (PANEL_DATABASE_URL)
  keygen <path>             create a key file: the signing key (PANEL_SIGNING_KEY)
                            or the data key (PANEL_DATA_KEY); make them separately
  org create <name>         add an org (until accounts exist)
  join-token <org-id> [name]  a single-use token that links one node (1 hour)
  rollout start <version>   update nodes' Wings in stages (5%, 25%, all)
  rollout status|pause|resume|cancel
  version                   print version

environment:
  PANEL_DATABASE_URL   Postgres; without it, serve api has no node routes
  PANEL_SIGNING_KEY    the signing key file (nodes pin its public key)
  PANEL_DATA_KEY       the key file that encrypts TOTP secrets; without it,
                       two-factor authentication is off
  PANEL_API_ADDR       listen address (default :8080)
  PANEL_APP_URL                the web app's origin (default https://app.raptorpanel.net);
                               the only origin browsers may call the API from
  PANEL_API_URL                the API's public origin (default https://api.raptorpanel.net),
                               for OAuth callbacks (<url>/oauth/<provider>/callback)
  PANEL_{GOOGLE,GITHUB,DISCORD}_CLIENT_ID, _CLIENT_SECRET
                               OAuth apps; each provider is offered once both are set
  PANEL_TURNSTILE_SECRET       Cloudflare Turnstile secret for the email sign-in form
  PANEL_MAIL_LOG=1             development only: write sign-in emails to the log
  PANEL_CLIENT_IP_HEADER       header with the client's address (CF-Connecting-IP);
                               only when the origin accepts nothing but Cloudflare
  PANEL_NODE_DOMAIN            node hostnames' domain (default raptornodes.net)
  PANEL_CLOUDFLARE_DNS_TOKEN   Cloudflare API token that can edit that zone's DNS
  PANEL_CLOUDFLARE_ZONE_ID     the zone's ID`

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
		fmt.Printf("key written to %s\npublic key (if it is the signing key): %s\n", args[1], base64.StdEncoding.EncodeToString(pub))
		return nil
	case len(args) == 3 && args[0] == "org" && args[1] == "create":
		return withRegistry(ctx, func(r *nodes.Registry) error {
			id, err := r.CreateOrg(ctx, args[2])
			if err == nil {
				fmt.Println(id)
			}
			return err
		})
	case len(args) >= 2 && args[0] == "rollout":
		return rolloutCmd(ctx, args[1:])
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
		updates := &rollout.Engine{DB: pool, Sender: router, PanelKey: reg.PanelKey, Log: log}
		go updates.Run(ctx, 30*time.Second)
		log.Info("panel instance", "id", router.ID)
		reg.KeyChanged = cfg.Hub.Disconnect
		cfg.AppOrigin = envOr("PANEL_APP_URL", "https://app.raptorpanel.net")
		cfg.Auth = &auth.Service{DB: pool, AppURL: cfg.AppOrigin, ClientIPHeader: os.Getenv("PANEL_CLIENT_IP_HEADER"), Log: log}
		if secret := os.Getenv("PANEL_TURNSTILE_SECRET"); secret != "" {
			cfg.Auth.Turnstile = auth.Turnstile{Secret: secret}
		}
		wa, err := auth.NewWebAuthn(cfg.AppOrigin)
		if err != nil {
			return fmt.Errorf("PANEL_APP_URL: %w", err)
		}
		cfg.Auth.WebAuthn = wa
		if path := os.Getenv("PANEL_DATA_KEY"); path != "" {
			key, err := nodelink.LoadKey(path)
			if err != nil {
				return fmt.Errorf("PANEL_DATA_KEY: %w", err)
			}
			if key == nil {
				return fmt.Errorf("%s doesn't exist (create it with panel keygen)", path)
			}
			// The file is 32 random bytes, in the signing key's format.
			cfg.Auth.DataKey = key.Seed()
		} else {
			log.Warn("PANEL_DATA_KEY is not set: two-factor authentication is off")
		}
		cfg.Auth.OAuth = oauthProviders(envOr("PANEL_API_URL", "https://api.raptorpanel.net"), log)
		go cfg.Auth.RunJanitor(ctx, time.Hour)
		if os.Getenv("PANEL_MAIL_LOG") == "1" {
			// Development: sign-in emails (with their codes) go to the log.
			cfg.Auth.Mailer = auth.LogMailer{Log: log}
		}
		addrs := &nodes.Addresses{Store: reg, Domain: envOr("PANEL_NODE_DOMAIN", "raptornodes.net"), Log: log}
		if tok := os.Getenv("PANEL_CLOUDFLARE_DNS_TOKEN"); tok != "" {
			addrs.DNS = &dns.Cloudflare{Token: tok, ZoneID: os.Getenv("PANEL_CLOUDFLARE_ZONE_ID")}
		} else {
			log.Warn("PANEL_CLOUDFLARE_DNS_TOKEN is not set: node hostnames aren't updated")
		}
		cfg.Hub.ClientIPHeader = os.Getenv("PANEL_CLIENT_IP_HEADER")
		cfg.Hub.OnAddress = addrs.Seen
		mirror := &nodes.Mirror{DB: pool, Hub: cfg.Hub, Log: log}
		cfg.Hub.EventsAvailable = func(_ context.Context, id string, _ int64) { mirror.Notify(id) }

	} else {
		log.Warn("PANEL_DATABASE_URL is not set: nodes can't enroll or connect")
	}
	return api.Run(ctx, cfg, log)
}

// oauthProviders are the OAuth apps configured in the environment.
func oauthProviders(apiURL string, log *slog.Logger) map[string]auth.OAuthProvider {
	out := map[string]auth.OAuthProvider{}
	for name, newProvider := range map[string]func(id, secret, redirect string) auth.OAuthProvider{
		"google":  func(id, secret, redirect string) auth.OAuthProvider { return auth.Google(id, secret, redirect) },
		"github":  func(id, secret, redirect string) auth.OAuthProvider { return auth.NewGitHub(id, secret, redirect) },
		"discord": func(id, secret, redirect string) auth.OAuthProvider { return auth.NewDiscord(id, secret, redirect) },
	} {
		env := "PANEL_" + strings.ToUpper(name)
		id, secret := os.Getenv(env+"_CLIENT_ID"), os.Getenv(env+"_CLIENT_SECRET")
		if id == "" || secret == "" {
			continue
		}
		out[name] = newProvider(id, secret, strings.TrimSuffix(apiURL, "/")+"/oauth/"+name+"/callback")
		log.Info("oauth sign-in on", "provider", name)
	}
	return out
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

func rolloutCmd(ctx context.Context, args []string) error {
	url := os.Getenv("PANEL_DATABASE_URL")
	if url == "" {
		return errors.New("PANEL_DATABASE_URL is not set")
	}
	pool, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	e := &rollout.Engine{DB: pool}
	switch {
	case args[0] == "start" && len(args) == 2:
		r, err := e.Start(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("rolling out %s: 5%% of nodes first; the Panel advances it (panel rollout status)\n", r.Version)
		return nil
	case args[0] == "status":
		s, err := e.Status(ctx)
		if err == nil {
			fmt.Println(s)
		}
		return err
	case args[0] == "pause":
		return e.SetState(ctx, "paused")
	case args[0] == "resume":
		return e.SetState(ctx, "running")
	case args[0] == "cancel":
		return e.SetState(ctx, "cancelled")
	}
	return errors.New("usage: panel rollout start <version> | status | pause | resume | cancel")
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
