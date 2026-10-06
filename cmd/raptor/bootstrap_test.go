package main

import (
	"encoding/json"
	"testing"
)

func TestMergeDaemonJSON(t *testing.T) {
	// A file with the owner's own settings: kept, Raptor's added.
	out, changed, restart, err := mergeDaemonJSON([]byte(`{"data-root": "/srv/docker", "live-restore": false}`))
	if err != nil || !changed || !restart {
		t.Fatalf("merge: changed %v restart %v err %v", changed, restart, err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["data-root"] != "/srv/docker" || m["live-restore"] != true || m["userland-proxy"] != false || m["log-driver"] != "local" {
		t.Errorf("merged: %s", out)
	}
	// Already merged: nothing to do.
	if _, changed, _, err := mergeDaemonJSON(out); err != nil || changed {
		t.Errorf("second merge: changed %v err %v", changed, err)
	}
	// Only live-restore missing: a reload is enough.
	delete(m, "live-restore")
	b, _ := json.Marshal(m)
	if _, changed, restart, _ := mergeDaemonJSON(b); !changed || restart {
		t.Errorf("live-restore only: changed %v restart %v", changed, restart)
	}
	// No file yet.
	if _, changed, restart, err := mergeDaemonJSON(nil); err != nil || !changed || !restart {
		t.Errorf("empty: changed %v restart %v err %v", changed, restart, err)
	}
	if _, _, _, err := mergeDaemonJSON([]byte("{not json")); err == nil {
		t.Error("broken daemon.json accepted")
	}
}
