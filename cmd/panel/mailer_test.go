package main

import (
	"log/slog"
	"testing"

	"github.com/xena-studios/raptor/internal/panel/auth"
)

func TestMailerConfig(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	for _, c := range []struct {
		key, from, toLog string
		want             string // "resend", "log", "none", or "error"
	}{
		{"re_x", "Raptor <a@mail.example.test>", "", "resend"},
		{"re_x", "", "", "error"},
		{"re_x", "Raptor <a@mail.example.test>", "1", "error"},
		{"", "", "1", "log"},
		{"", "", "", "none"},
	} {
		t.Setenv("PANEL_RESEND_API_KEY", c.key)
		t.Setenv("PANEL_MAIL_FROM", c.from)
		t.Setenv("PANEL_MAIL_LOG", c.toLog)
		m, err := mailer(log)
		got := "none"
		switch m.(type) {
		case *auth.Resend:
			got = "resend"
		case auth.LogMailer:
			got = "log"
		}
		if err != nil {
			got = "error"
		}
		if got != c.want {
			t.Errorf("%+v: got %s (%v)", c, got, err)
		}
	}
}
