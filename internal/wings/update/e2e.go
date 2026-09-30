//go:build e2eupdate

package update

import (
	"os"
	"time"
)

// Builds for the self-update e2e test (task e2e:update, scripts/e2e-update.sh)
// take the release key, the release server, and trial timings from the
// environment, and can be built broken on purpose. Release builds never
// include this file.

// e2eBreak, set with -ldflags, makes a version fail its trial: "crash" exits
// right away, "hang" never reports healthy.
var e2eBreak string

func init() {
	if k := os.Getenv("RAPTOR_E2E_RELEASE_KEY"); k != "" {
		releaseKey = k
	}
	if u := os.Getenv("RAPTOR_E2E_RELEASE_URL"); u != "" {
		releaseAPI, releaseDownload = u, u
	}
	if d, err := time.ParseDuration(os.Getenv("RAPTOR_E2E_TRIAL_TIMEOUT")); err == nil {
		TrialTimeout = d
	}
	if d, err := time.ParseDuration(os.Getenv("RAPTOR_E2E_SETTLE")); err == nil {
		settleTime = d
	}
	if os.Getenv(trialEnv) == "" {
		return
	}
	switch e2eBreak {
	case "crash":
		os.Exit(3)
	case "hang":
		readyOnce.Do(func() {})
	}
}
