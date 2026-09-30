package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Outcomes of an update.
const (
	StatusTrial     = "trial"     // installed; the next start runs it on trial
	StatusSucceeded = "succeeded" // healthy on trial and made current
	StatusFailed    = "failed"    // rolled back to the previous version
)

// State is the last update. It's a small file next to the state database
// rather than a table in it, because the launcher reads and writes it before
// Wings opens the database (and the database may be newer than the launcher).
type State struct {
	Status   string    `json:"status"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Actor    string    `json:"actor,omitempty"`
	Attempts int       `json:"attempts,omitempty"` // trial starts so far
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	Error    string    `json:"error,omitempty"`
	// Reported: the outcome was recorded as an event.
	Reported bool `json:"reported,omitempty"`
}

// StatePath is where the update state is kept, given the state database's path.
func StatePath(stateDB string) string {
	return filepath.Join(filepath.Dir(stateDB), "update.json")
}

// ReadState reads the update state. No file means no update has happened.
func ReadState(path string) (State, error) {
	var s State
	data, err := os.ReadFile(path) //nolint:gosec // from config
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// WriteState replaces the update state atomically, root-only.
func WriteState(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) //nolint:gosec // from config
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
