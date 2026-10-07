package doctor

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Upload sends a bundle to the Panel (panelURL, the node connection's
// https URL) and returns the support code it's filed under. A linked node
// (nodeID and key set) signs it, so support sees which node it's from.
// Unlinked nodes can upload too: they're often the ones needing help.
func Upload(ctx context.Context, client *http.Client, panelURL, path, nodeID string, key ed25519.PrivateKey, now time.Time) (string, error) {
	body, err := os.ReadFile(path) //nolint:gosec // the bundle doctor just wrote
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(panelURL, "/")+nodelink.BundlePath, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/gzip")
	if nodeID != "" && key != nil {
		for k, v := range nodelink.SignBundle(key, nodeID, now, body) {
			req.Header.Set(k, v)
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("couldn't reach the Panel: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out); err != nil {
		return "", fmt.Errorf("the Panel answered %s", res.Status)
	}
	switch {
	case out.Error != "":
		return "", errors.New(out.Error)
	case res.StatusCode != http.StatusOK || out.Code == "":
		return "", fmt.Errorf("the Panel answered %s", res.Status)
	}
	return out.Code, nil
}
