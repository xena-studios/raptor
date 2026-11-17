package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/alerts"
)

// A push setting that's there but wrong turns that push off and says so;
// it doesn't stop the Panel (v0.18.0's first deploy).
func TestPushServiceBadSettings(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	t.Setenv("PANEL_APNS_KEY", "not a key")
	t.Setenv("PANEL_APNS_KEY_ID", "ABCDEFGHIJ")
	t.Setenv("PANEL_APNS_TEAM_ID", "ABCDEFGHIJ")
	t.Setenv("PANEL_FCM_CREDENTIALS", "None")
	p := pushService(log)
	if p == nil || p.Offers(alerts.IOS) || p.Offers(alerts.Android) {
		t.Fatalf("push %+v", p)
	}
	for _, want := range []string{"PANEL_APNS_KEY isn't a .p8 key", "PANEL_FCM_CREDENTIALS isn't a service account's JSON key"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log lacks %q: %s", want, buf.String())
		}
	}
}

// closePool returns even when a connection is never given back.
func TestClosePoolBounded(t *testing.T) {
	url := "postgres://raptor:raptor@127.0.0.1:54321/raptor_test"
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skip(err)
	}
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		pool.Close()
		t.Skip("no test database:", err)
	}
	defer conn.Release()
	start := time.Now()
	closePoolWithin(pool, slog.New(slog.DiscardHandler), 300*time.Millisecond)
	if d := time.Since(start); d < 250*time.Millisecond || d > 5*time.Second {
		t.Errorf("returned after %s, want about 300ms", d)
	}
}
