package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1/localv1connect"
	"github.com/xena-studios/raptor/internal/wings/update"
)

func updateCmd(ctx context.Context, args []string) error {
	var check *bool
	var version *string
	c, pos, err := dial("update", args, func(fs *flag.FlagSet) {
		check = fs.Bool("check", false, "only show what would be installed")
		version = fs.String("version", "", "install this version (e.g. 1.4.2) instead of the newest in the channel")
	})
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return errors.New("usage: raptor update [-check] [-version v]")
	}

	start := time.Now()
	rctx, cancel := context.WithTimeout(ctx, 5*time.Minute) // downloading
	defer cancel()
	res, err := c.Update(rctx, &localv1.UpdateRequest{Check: *check, Version: *version})
	if err != nil {
		return rpcErr(err)
	}
	from := ""
	if ch := res.GetChannel(); ch == "stable" || ch == "beta" {
		from = " (" + ch + " channel)"
	} else if ch == "pin" {
		from = " (pinned in config.yml)"
	}
	switch {
	case !res.GetAvailable():
		fmt.Printf("Wings %s is up to date%s\n", res.GetCurrent(), from)
		return nil
	case *check:
		fmt.Printf("Wings %s is installed; %s is available%s. Run raptor update as root to install it.\n", res.GetCurrent(), res.GetTarget(), from)
		return nil
	}

	fmt.Printf("Verified Wings %s%s. Restarting to try it; servers keep running.\n", res.GetTarget(), from)
	return followUpdate(ctx, c, res.GetTarget(), start)
}

// followUpdate waits for the outcome of an update to target.
func followUpdate(ctx context.Context, c localv1connect.LocalServiceClient, target string, since time.Time) error {
	// The trial, plus restarts.
	wait := update.TrialTimeout + 3*time.Minute
	deadline := time.Now().Add(wait)
	said := ""
	say := func(s string) {
		if s != said {
			fmt.Fprintln(os.Stderr, s)
			said = s
		}
	}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "Stopped following; the update carries on (see raptor status).")
			return nil
		case <-time.After(2 * time.Second):
		}
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		st, err := c.GetStatus(sctx, &localv1.GetStatusRequest{})
		cancel()
		if err != nil {
			say("Waiting for Wings to restart…")
			continue
		}
		u := st.GetUpdate()
		if u.GetTo() != target || u.GetStartedAt().AsTime().Before(since.Add(-time.Second)) {
			continue
		}
		switch u.GetStatus() {
		case update.StatusTrial:
			say(fmt.Sprintf("Trying Wings %s (up to %s)…", target, update.TrialTimeout))
		case update.StatusSucceeded:
			fmt.Printf("✓ Updated Wings %s → %s\n", u.GetFrom(), target)
			return nil
		case update.StatusFailed:
			return fmt.Errorf("the update to %s failed, so Wings is back on %s: %s", target, u.GetFrom(), u.GetError())
		}
	}
	return fmt.Errorf("no outcome after %s; see raptor status and journalctl -u raptor-wings", wait)
}
