package nodes

import (
	"context"
	"crypto/ed25519"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
)

// Bootstrap reports its steps with the join token; the org sees them, and
// the node once it links. A step keeps the time it got to its state.
func TestJoinProgress(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	org, err := r.CreateOrg(ctx, "test org")
	if err != nil {
		t.Fatal(err)
	}
	orgID := pgUUID(uuid.MustParse(org))
	token, err := r.CreateJoinToken(ctx, org, "")
	if err != nil {
		t.Fatal(err)
	}
	report := func(steps ...*nodev1.BootstrapStep) error {
		_, err := r.ReportProgress(ctx, &nodev1.ReportProgressRequest{Token: token, Steps: steps})
		return err
	}
	if err := report(&nodev1.BootstrapStep{Name: "Docker", State: "running"}); err != nil {
		t.Fatal(err)
	}
	steps, node, err := r.JoinProgress(ctx, orgID, token)
	if err != nil || len(steps) != 1 || steps[0].State != "running" || node != "" {
		t.Fatalf("running: %+v %q %v", steps, node, err)
	}
	first := steps[0].At
	if err := report(&nodev1.BootstrapStep{Name: "Docker", State: "done"}, &nodev1.BootstrapStep{Name: "Config", State: "failed", Detail: "disk full"}); err != nil {
		t.Fatal(err)
	}
	steps, _, _ = r.JoinProgress(ctx, orgID, token)
	if len(steps) != 2 || steps[0].State != "done" || steps[1].Detail != "disk full" || steps[0].At.Before(first) {
		t.Errorf("after two steps: %+v", steps)
	}

	for name, req := range map[string]*nodev1.ReportProgressRequest{
		"an unknown token": {Token: "rpt_join_nope", Steps: []*nodev1.BootstrapStep{{Name: "x", State: "done"}}},
		"not a token":      {Token: "hello"},
	} {
		if _, err := r.ReportProgress(ctx, req); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := report(&nodev1.BootstrapStep{Name: "x", State: "exploded"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a made-up state: %v", err)
	}
	other, _ := r.CreateOrg(ctx, "other")
	if _, _, err := r.JoinProgress(ctx, pgUUID(uuid.MustParse(other)), token); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("another org's token: %v", err)
	}

	_, key, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, key))
	if err != nil {
		t.Fatal(err)
	}
	if _, node, _ := r.JoinProgress(ctx, orgID, token); node != res.GetNodeId() {
		t.Errorf("node after linking: %q, want %q", node, res.GetNodeId())
	}
}
