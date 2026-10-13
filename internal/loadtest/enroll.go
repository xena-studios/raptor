package loadtest

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"net/http"
	"strings"
	"sync"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Enroll links a node for each join token through the Panel's enrollment
// API, as `raptor link` does, a few at a time.
func Enroll(ctx context.Context, panelURL string, client *http.Client, tokens []string, parallel int) ([]Identity, error) {
	api := nodev1connect.NewEnrollmentServiceClient(client, strings.TrimSuffix(panelURL, "/")+"/api")
	out := make([]Identity, len(tokens))
	errs := make([]error, len(tokens))
	sem := make(chan struct{}, max(parallel, 1))
	var wg sync.WaitGroup
	for i, token := range tokens {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			pub, key, err := ed25519.GenerateKey(nil)
			if err != nil {
				errs[i] = err
				return
			}
			payload, err := nodelink.EnrollPayload(token, pub)
			if err != nil {
				errs[i] = err
				return
			}
			res, err := api.Enroll(ctx, &nodev1.EnrollRequest{
				Token: token, PublicKey: pub, Signature: ed25519.Sign(key, payload),
				Name: fmt.Sprintf("loadtest-%04d", i+1), WingsVersion: "loadtest",
			})
			if err != nil {
				errs[i] = fmt.Errorf("enrolling node %d: %w", i+1, err)
				return
			}
			out[i] = Identity{NodeID: res.GetNodeId(), Key: key, PanelKey: res.GetPanelKey()}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
