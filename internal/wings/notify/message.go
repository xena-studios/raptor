package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/host"
)

// Levels, for colors and for receivers that filter.
const (
	Info     = "info"
	Warning  = "warning"
	Critical = "critical"
)

// Message is one notification.
type Message struct {
	Category string         `json:"category"` // config.NotificationCategories
	Level    string         `json:"level"`
	Type     string         `json:"type"` // the event or audit action
	Title    string         `json:"title"`
	Text     string         `json:"text"`
	ServerID string         `json:"server_id,omitempty"`
	Server   string         `json:"server_name,omitempty"`
	At       time.Time      `json:"at"`
	Data     map[string]any `json:"data,omitempty"`
}

func str(d map[string]any, k string) string {
	if v, ok := d[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func num(d map[string]any, k string) int64 {
	switch v := d[k].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// FromEvent turns an event into a message; false for events nobody is
// notified about.
func FromEvent(e events.Event) (Message, bool) {
	m := Message{Type: e.Type, ServerID: e.ServerID, At: e.At, Data: e.Data, Level: Warning}
	d := e.Data
	switch e.Type {
	case "server.crashed":
		m.Category, m.Title = "crash", "Server crashed"
		m.Text = fmt.Sprintf("Exit code %d (%s).", num(d, "exit_code"), str(d, "reason"))
		if c, ok := d["console"].([]any); ok && len(c) > 0 {
			lines := make([]string, 0, len(c))
			for _, l := range c[max(0, len(c)-10):] {
				lines = append(lines, fmt.Sprint(l))
			}
			m.Text += "\nLast lines:\n" + strings.Join(lines, "\n")
		}
	case "server.crash_loop":
		m.Category, m.Level, m.Title = "crash", Critical, "Server stopped after repeated crashes"
		m.Text = fmt.Sprintf("It crashed %d times in a row and won't be restarted until someone starts it.", num(d, "crashes"))
	case "server.install.failed":
		m.Category, m.Title = "install", "Install failed"
		m.Text = str(d, "error")
	case "server.delete.failed":
		m.Category, m.Title = "backup", "Server not deleted: its final backup failed"
		m.Text = str(d, "error")
	case "backup.finished":
		if str(d, "status") != "failed" {
			return m, false
		}
		m.Category, m.Title = "backup", "Backup failed"
		m.Text = str(d, "error")
	case "backup.restore.finished":
		if ok, _ := d["ok"].(bool); ok {
			m.Category, m.Level, m.Title = "backup", Info, "Backup restored"
		} else {
			m.Category, m.Title = "backup", "Restore failed"
			m.Text = str(d, "error")
		}
	case "server.disk_limit_exceeded":
		m.Category, m.Title = "disk", "Server over its disk limit"
		m.Text = fmt.Sprintf("Using %s of %s. It's stopped if it's still over at the next check.", host.Bytes(num(d, "used_bytes")), host.Bytes(num(d, "limit_bytes")))
	case "node.disk_low":
		m.Category, m.Level, m.Title = "disk", Critical, "Host disk low"
		m.Text = fmt.Sprintf("Below %s free: installs and image pulls are refused until space is freed.", host.Bytes(num(d, "min_free")))
	case "node.disk_ok":
		m.Category, m.Level, m.Title = "disk", Info, "Host disk has enough free space again"
	case "node.update":
		m.Category, m.Level = "update", Info
		from, to := str(d, "from"), str(d, "to")
		if str(d, "result") == "succeeded" {
			m.Title = fmt.Sprintf("Wings updated to %s", to)
			m.Text = "From " + from + "."
		} else {
			m.Level, m.Title = Warning, fmt.Sprintf("Wings update to %s rolled back", to)
			m.Text = fmt.Sprintf("Still on %s: %s", from, str(d, "error"))
		}
	default:
		return m, false
	}
	return m, true
}

// FromAudit turns an audit entry (a signed dangerous action, a pairing, or
// a key reset) into a security message.
func FromAudit(a command.AuditEntry) Message {
	m := Message{Category: "security", Type: a.Action, ServerID: a.ServerID, At: a.At, Level: Warning}
	who := a.UserID
	if a.KeyName != "" || a.Fingerprint != "" {
		who += " with key " + strings.TrimSpace(a.KeyName+" "+a.Fingerprint)
	}
	switch {
	case a.Action == "keys.reset":
		m.Level, m.Title = Critical, "Trusted passkeys reset on the node"
		m.Text = fmt.Sprintf("By %s: %s.", a.UserID, a.Detail)
	case a.Outcome == command.AuditRejected:
		m.Level, m.Title = Critical, "Signed action rejected: "+a.Action
		m.Text = fmt.Sprintf("Requested by %s. Rejected: %s. If nobody you know did this, someone may be sending commands through the Panel.", who, a.Detail)
	case a.Outcome == command.AuditFailed:
		m.Title = "Signed action failed: " + a.Action
		m.Text = fmt.Sprintf("By %s: %s", who, a.Detail)
	default:
		m.Title = "Signed action: " + a.Action
		m.Text = "By " + who + "."
		if a.Detail != "" {
			m.Text += " " + a.Detail
		}
	}
	m.Data = map[string]any{"command_id": a.CommandID, "outcome": a.Outcome, "user_id": a.UserID, "key_fingerprint": a.Fingerprint}
	return m
}
