package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1/localv1connect"
)

func keysCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: raptor keys list|reset")
	}
	switch args[0] {
	case "list":
		return keysList(ctx, args[1:])
	case "reset":
		return keysReset(ctx, args[1:], os.Stdin, os.Stderr, isTerminal(os.Stdin))
	}
	return errors.New("usage: raptor keys list|reset")
}

func keysList(ctx context.Context, args []string) error {
	var asJSON *bool
	c, _, err := dial("keys list", args, func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") })
	if err != nil {
		return err
	}
	res, err := c.ListKeys(ctx, &localv1.ListKeysRequest{})
	if err != nil {
		return rpcErr(err)
	}
	if *asJSON {
		return printProtoJSON(res)
	}
	if len(res.GetKeys()) == 0 {
		fmt.Println("No passkeys are trusted on this node yet: signed actions (deleting servers, restoring backups…) are refused.")
		fmt.Println("An owner key is pinned when the node is linked to the Panel, or with raptor keys reset.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "FINGERPRINT\tROLE\tUSER\tNAME\tSCOPE\tADDED")
	for _, k := range res.GetKeys() {
		scope := "every signed action"
		if k.GetRole() == "delegate" {
			server := "every server"
			if k.GetServerId() != "" {
				server = short(k.GetServerId())
			}
			scope = strings.Join(k.GetActions(), ",") + " on " + server
			if k.GetExpiresAt() != nil {
				scope += ", until " + k.GetExpiresAt().AsTime().Local().Format("2006-01-02 15:04")
			}
		}
		added := k.GetAddedAt().AsTime().Local().Format("2006-01-02")
		if k.GetAddedBy() == "" {
			added += " (pinned on the box)"
		} else {
			added += " by " + k.GetAddedBy()
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", k.GetFingerprint(), k.GetRole(), k.GetUserId(), k.GetName(), scope, added)
	}
	return w.Flush()
}

// keysReset re-pairs the node's owner passkey: it shows a code for the
// Panel, waits for a key to pair, and pins it once root confirms its
// fingerprint matches the one the browser shows.
func keysReset(ctx context.Context, args []string, in io.Reader, out io.Writer, tty bool) error {
	c, _, err := dial("keys reset", args, nil)
	if err != nil {
		return err
	}
	if !tty {
		return errors.New("a key reset needs a terminal: you'll compare a fingerprint and confirm it")
	}
	r := bufio.NewReader(in)
	_, _ = fmt.Fprintln(out, "This replaces every passkey this node trusts (owners and delegations) with one new owner passkey.")
	_, _ = fmt.Fprintln(out, "Use it when the owner's passkeys are lost. Nothing changes until you confirm the new key below.")
	_, _ = fmt.Fprint(out, "Type reset to continue: ")
	if line, _ := r.ReadString('\n'); strings.TrimSpace(line) != "reset" {
		return errors.New("not reset")
	}
	start, err := c.StartKeyReset(ctx, &localv1.StartKeyResetRequest{})
	if err != nil {
		return rpcErr(err)
	}
	id := start.GetResetId()
	cancel := func() {
		cctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = c.CancelKeyReset(cctx, &localv1.CancelKeyResetRequest{ResetId: id})
	}
	_, _ = fmt.Fprintf(out, "\n    Pairing code: %s\n\n", start.GetCode())
	_, _ = fmt.Fprintf(out, "In the Panel, open this node's settings, choose \"Pair a passkey\", enter the code, and sign with the new passkey.\n")
	_, _ = fmt.Fprintf(out, "The code works once, until %s. Waiting… (Ctrl-C cancels)\n", start.GetExpiresAt().AsTime().Local().Format("15:04"))

	st, err := waitPairing(ctx, c, id)
	if err != nil {
		cancel()
		return err
	}
	_, _ = fmt.Fprintf(out, "\nA passkey paired: %q for user %s\n\n    Fingerprint: %s\n\n", st.GetName(), st.GetUserId(), st.GetFingerprint())
	_, _ = fmt.Fprint(out, "Does the browser show exactly this fingerprint? Pin this key and remove all others? [y/N] ")
	line, _ := r.ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		cancel()
		return errors.New("not pinned: the key reset was cancelled, and no key changed. If the fingerprints differed, don't use that Panel session")
	}
	if _, err := c.ConfirmKeyReset(ctx, &localv1.ConfirmKeyResetRequest{ResetId: id, Fingerprint: st.GetFingerprint()}); err != nil {
		return rpcErr(err)
	}
	_, _ = fmt.Fprintln(out, "Pinned. It's now this node's only trusted passkey; add more devices from the Panel.")
	return nil
}

// waitPairing polls a key reset until a key paired, it expired, or ctx ends.
func waitPairing(ctx context.Context, c localv1connect.LocalServiceClient, id string) (*localv1.GetKeyResetResponse, error) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		st, err := c.GetKeyReset(ctx, &localv1.GetKeyResetRequest{ResetId: id})
		if err != nil {
			return nil, rpcErr(err)
		}
		switch st.GetState() {
		case "pending":
			return st, nil
		case "expired":
			return nil, errors.New("the key reset expired or was cancelled (too many wrong codes, or another reset started); no key changed")
		}
		select {
		case <-ctx.Done():
			return nil, errors.New("cancelled; no key changed")
		case <-t.C:
		}
	}
}

func auditCmd(ctx context.Context, args []string) error {
	var asJSON *bool
	var n *int
	var since *time.Duration
	c, _, err := dial("audit", args, func(fs *flag.FlagSet) {
		asJSON = fs.Bool("json", false, "print JSON")
		n = fs.Int("n", 50, "how many entries")
		since = fs.Duration("since", 0, "only entries this recent, e.g. 72h")
	})
	if err != nil {
		return err
	}
	req := &localv1.ListAuditRequest{Limit: int32(min(*n, 10000))} //nolint:gosec // capped
	if *since > 0 {
		req.Since = timestamppb.New(time.Now().Add(-*since))
	}
	res, err := c.ListAudit(ctx, req)
	if err != nil {
		return rpcErr(err)
	}
	if *asJSON {
		return printProtoJSON(res)
	}
	if len(res.GetEntries()) == 0 {
		fmt.Println("No signed actions recorded.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TIME\tACTION\tSERVER\tUSER\tKEY\tOUTCOME")
	for _, a := range res.GetEntries() {
		key := a.GetKeyName()
		if a.GetKeyFingerprint() != "" {
			key = strings.TrimSpace(key + " " + a.GetKeyFingerprint())
		}
		if key == "" {
			key = "-"
		}
		server := "-"
		if a.GetServerId() != "" {
			server = short(a.GetServerId())
		}
		outcome := a.GetOutcome()
		if a.GetDetail() != "" {
			outcome += ": " + a.GetDetail()
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a.GetAt().AsTime().Local().Format("2006-01-02 15:04:05"),
			a.GetAction(), server, a.GetUserId(), key, outcome)
	}
	return w.Flush()
}

func printProtoJSON(m proto.Message) error {
	b, err := protojson.MarshalOptions{Multiline: true}.Marshal(m)
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(b))
	return err
}
