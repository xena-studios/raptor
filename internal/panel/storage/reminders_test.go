package storage

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/hosted"
)

type mailbox struct {
	mu   sync.Mutex
	sent []string // "to\nsubject\ntext"
}

func (m *mailbox) Send(_ context.Context, to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"\n"+subject+"\n"+text)
	return nil
}

func (m *mailbox) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.sent
	m.sent = nil
	return out
}

func TestDue(t *testing.T) {
	off := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	purge := off.Add(PurgeAfter)
	for _, c := range []struct {
		notified []string
		at       time.Time
		want     string
	}{
		{nil, off, "off"},
		{[]string{"off"}, off.Add(time.Hour), ""},
		{[]string{"off"}, purge.Add(-8 * 24 * time.Hour), ""},
		{[]string{"off"}, purge.Add(-7 * 24 * time.Hour), "7d"},
		{[]string{"off", "7d"}, purge.Add(-2 * 24 * time.Hour), ""},
		{[]string{"off", "7d"}, purge.Add(-23 * time.Hour), "1d"},
		{[]string{"off", "7d", "1d"}, purge.Add(-time.Hour), ""},
		// Missed (the Panel was down): only the latest goes.
		{nil, purge.Add(-time.Hour), "1d"},
	} {
		got, ok := due(c.notified, purge, c.at)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("notified %v at %v: %q, want %q", c.notified, purge.Sub(c.at), got, c.want)
		}
	}
}

// The owners are emailed when a folder is turned off, 7 days before its
// data is deleted, and the day before; once each. Turning it back on in
// time keeps the data, and stops the reminders.
func TestReminders(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	fb := &fakeB2{keys: map[string]string{}, files: map[string]int64{}, hidden: map[string]int64{}}
	srv := httptest.NewServer(fb.handler(t))
	defer srv.Close()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	mail := &mailbox{}
	s := &Service{
		DB: db, B2: &B2{KeyID: "master", Key: "master-secret", AuthURL: srv.URL + "/b2api/v3/b2_authorize_account"},
		BucketID: "bucket-1", Endpoint: "s3.us-west-004.backblazeb2.com", Region: "us-west-004",
		Sender: &sender{}, PanelKey: key, Mailer: mail, AppURL: "https://app.example",
		Now: func() time.Time { return now },
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var org, owner, n1, n2 uuid.UUID
	if err := db.QueryRow(ctx, "INSERT INTO orgs (name) VALUES ('Acme') RETURNING id").Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "INSERT INTO users (email) VALUES ('owner@example.com') RETURNING id").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO users (email) VALUES ('member@example.com')")
	exec("INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')", org, owner)
	exec("INSERT INTO org_members (org_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@example.com'", org)
	node := func(name string) uuid.UUID {
		var id uuid.UUID
		if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, $2, decode(repeat('00', 32), 'hex')) RETURNING id`, org, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	n1, n2 = node("alpha"), node("beta")
	for _, n := range []uuid.UUID{n1, n2} {
		if err := s.Enable(ctx, org, n, pgtype.UUID{}); err != nil {
			t.Fatal(err)
		}
		fb.files[hosted.Prefix(org.String(), n.String())+"p"] = 100
	}
	tick := func() {
		t.Helper()
		if err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// alpha turned off; beta's node removed.
	if err := s.Disable(ctx, n1); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE nodes SET deleted_at = now() WHERE id = $1", n2)
	if err := s.Disable(ctx, n2); err != nil {
		t.Fatal(err)
	}
	tick()
	got := mail.take()
	if len(got) != 2 {
		t.Fatalf("at turn-off: %q", got)
	}
	for _, m := range got {
		if !strings.HasPrefix(m, "owner@example.com\n") || !strings.Contains(m, "November 9, 2026") || !strings.Contains(m, "Acme") ||
			!strings.Contains(m, "https://app.example/orgs/"+org.String()+"/settings/backups") {
			t.Errorf("a turn-off email: %q", m)
		}
	}
	if !strings.Contains(strings.Join(got, "\n"), "beta was removed") || !strings.Contains(strings.Join(got, "\n"), "turned off for alpha") {
		t.Errorf("turn-off emails: %q", got)
	}
	tick()
	if got := mail.take(); len(got) != 0 {
		t.Errorf("again: %q", got)
	}

	// alpha is turned back on: its data stays, and it hears nothing more.
	now = now.Add(10 * 24 * time.Hour)
	if err := s.Enable(ctx, org, n1, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(13 * 24 * time.Hour) // 7 days before the deletion
	tick()
	got = mail.take()
	if len(got) != 1 || !strings.Contains(got[0], "beta's offsite backups are deleted in 7 days") || !strings.Contains(got[0], "Recover backups") {
		t.Fatalf("7 days before: %q", got)
	}
	now = now.Add(6*24*time.Hour + time.Hour)
	tick()
	got = mail.take()
	if len(got) != 1 || !strings.Contains(got[0], "beta's offsite backups are deleted tomorrow") {
		t.Fatalf("the day before: %q", got)
	}
	now = now.Add(24 * time.Hour)
	tick()
	if got := mail.take(); len(got) != 0 {
		t.Errorf("after the deletion: %q", got)
	}
	if _, ok := fb.files[hosted.Prefix(org.String(), n2.String())+"p"]; ok {
		t.Error("beta's data wasn't deleted")
	}
	if _, ok := fb.files[hosted.Prefix(org.String(), n1.String())+"p"]; !ok {
		t.Error("alpha's data was deleted after it was turned back on")
	}
}
