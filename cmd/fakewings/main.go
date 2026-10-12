// Command fakewings load-tests a Panel with fake nodes
// (docs/RELIABILITY.md#load-tests). Never point it at production: setup
// writes to the Panel's database directly, and the nodes it makes are real
// nodes in a real org.
//
//	fakewings setup   -db <url> -panel <url> -nodes 1000 -out nodes.json
//	fakewings run     -panel <url> -in nodes.json -servers 5 -every 10s -for 10m
//	fakewings cleanup -db <url> -in nodes.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xena-studios/raptor/internal/loadtest"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// fleet is setup's output: the org the nodes are in, and the nodes.
type fleet struct {
	Panel string              `json:"panel"`
	OrgID string              `json:"org_id"`
	Nodes []loadtest.Identity `json:"nodes"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "setup":
		err = setup(ctx, os.Args[2:])
	case "run":
		err = run(ctx, os.Args[2:])
	case "cleanup":
		err = cleanup(ctx, os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakewings:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: fakewings setup|run|cleanup [flags] (fakewings <command> -h for its flags)")
	os.Exit(2)
}

// production is refused: a load test there makes thousands of real nodes
// in a real org, and its traffic is the users'.
func checkPanel(panel string) error {
	u, err := url.Parse(panel)
	if err != nil || u.Host == "" {
		return fmt.Errorf("-panel %q: want the Panel's URL, like http://localhost:8080", panel)
	}
	if h := strings.ToLower(u.Hostname()); h == "raptorpanel.net" || strings.HasSuffix(h, ".raptorpanel.net") {
		return errors.New("that's production: load-test a staging or local Panel instead")
	}
	return nil
}

func setup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	db := fs.String("db", "", "the Panel's Postgres URL (required; join tokens are made there)")
	panel := fs.String("panel", "", "the Panel's URL (required; the nodes enroll through it)")
	count := fs.Int("nodes", 100, "how many nodes")
	out := fs.String("out", "nodes.json", "where to write the nodes' identities (keep it private: they're the nodes' keys)")
	parallel := fs.Int("parallel", 20, "enrollments at once")
	_ = fs.Parse(args)
	if *db == "" || *panel == "" {
		return errors.New("-db and -panel are required")
	}
	if err := checkPanel(*panel); err != nil {
		return err
	}
	pool, err := store.Open(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()
	reg := &nodes.Registry{DB: pool}
	org, err := reg.CreateOrg(ctx, "Load test "+time.Now().UTC().Format("2006-01-02 15:04"))
	if err != nil {
		return err
	}
	tokens := make([]string, *count)
	for i := range tokens {
		if tokens[i], err = reg.CreateJoinToken(ctx, org, ""); err != nil {
			return err
		}
	}
	start := time.Now()
	ids, err := loadtest.Enroll(ctx, *panel, http.DefaultClient, tokens, *parallel)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(fleet{Panel: *panel, OrgID: org, Nodes: ids}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("enrolled %d nodes in org %s in %s; identities in %s\n", len(ids), org, time.Since(start).Round(time.Millisecond), *out)
	return nil
}

func load(path string) (fleet, error) {
	var f fleet
	b, err := os.ReadFile(path) //nolint:gosec // the operator names the file
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(b, &f)
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	in := fs.String("in", "nodes.json", "setup's output")
	panel := fs.String("panel", "", "the Panel's URL (default: the one setup used)")
	servers := fs.Int("servers", 5, "fake servers per node")
	every := fs.Duration("every", 10*time.Second, "how often each node changes a server and reports it (0: never)")
	ramp := fs.Duration("ramp", 30*time.Second, "how long to take to link every node")
	duration := fs.Duration("for", 5*time.Minute, "how long to run")
	report := fs.Duration("report", 10*time.Second, "how often to print progress")
	_ = fs.Parse(args)
	f, err := load(*in)
	if err != nil {
		return err
	}
	if *panel == "" {
		*panel = f.Panel
	}
	if err := checkPanel(*panel); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()
	// Every node is its own connection, as in a fleet.
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: true}}
	stats := loadtest.NewStats()
	var wg sync.WaitGroup
	gap := *ramp / time.Duration(max(len(f.Nodes), 1))
	go func() {
		t := time.NewTicker(*report)
		defer t.Stop()
		start := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fmt.Printf("— %s\n%s", time.Since(start).Round(time.Second), stats.Snapshot())
			}
		}
	}()
	for _, id := range f.Nodes {
		n := loadtest.NewNode(id, loadtest.Load{Servers: *servers, EventEvery: *every}, stats)
		wg.Go(func() { n.Run(ctx, *panel, client) })
		select {
		case <-ctx.Done():
		case <-time.After(gap):
		}
	}
	wg.Wait()
	fmt.Printf("— done\n%s", stats.Snapshot())
	return nil
}

func cleanup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	db := fs.String("db", "", "the Panel's Postgres URL (required)")
	in := fs.String("in", "nodes.json", "setup's output")
	_ = fs.Parse(args)
	if *db == "" {
		return errors.New("-db is required")
	}
	f, err := load(*in)
	if err != nil {
		return err
	}
	pool, err := store.Open(ctx, *db)
	if err != nil {
		return err
	}
	defer pool.Close()
	reg := &nodes.Registry{DB: pool}
	removed := 0
	for _, n := range f.Nodes {
		ok, err := reg.Remove(ctx, n.NodeID)
		if err != nil {
			return err
		}
		if ok {
			removed++
		}
	}
	fmt.Printf("removed %d nodes from org %s (the org stays, empty)\n", removed, f.OrgID)
	return nil
}
