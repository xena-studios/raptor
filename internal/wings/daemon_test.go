package wings

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
	"github.com/xena-studios/raptor/internal/wings/update"
)

func TestReportUpdate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	o := events.New(db)
	path := filepath.Join(dir, "update.json")
	log := slog.New(slog.DiscardHandler)

	// Nothing to report while on trial.
	st := update.State{Status: update.StatusTrial, From: "1.0.0", To: "1.1.0", Actor: "local:root", Started: time.Now()}
	if err := update.WriteState(path, st); err != nil {
		t.Fatal(err)
	}
	reportUpdate(ctx, path, o, log)
	if n, _ := o.Last(ctx); n != 0 {
		t.Fatalf("%d events", n)
	}

	st.Status, st.Error, st.Finished = update.StatusFailed, "not healthy within 5m0s", time.Now()
	if err := update.WriteState(path, st); err != nil {
		t.Fatal(err)
	}
	reportUpdate(ctx, path, o, log)
	reportUpdate(ctx, path, o, log) // once only
	evs, err := o.Since(ctx, 0, 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("%v %v", evs, err)
	}
	e := evs[0]
	if e.Type != "node.update" || e.Data["result"] != "failed" || e.Data["to"] != "1.1.0" || e.Data["actor"] != "local:root" || e.Data["error"] != st.Error {
		t.Fatalf("%+v", e)
	}
	if got, _ := update.ReadState(path); !got.Reported {
		t.Fatal("not marked reported")
	}
}
