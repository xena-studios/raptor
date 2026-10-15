package alerts

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Levels, for colors and for receivers that filter.
const (
	Info     = "info"
	Warning  = "warning"
	Critical = "critical"
)

// Condition is what a rule watches for.
type Condition struct {
	ID    string
	Label string
	// Scope: "server" conditions can apply to one server, a node, or the
	// org; "node" ones to a node or the org.
	Scope string
	// Threshold conditions are evaluated by Wings (it has the metrics) and
	// take a threshold and a duration; Unit is the threshold's.
	Threshold bool
	Unit      string
	// Resolves: the condition ends with a "resolved" alert.
	Resolves bool
}

// Conditions are every condition a rule can have.
var Conditions = []Condition{
	{ID: "crash", Label: "Server crashed", Scope: "server"},
	{ID: "crash_loop", Label: "Server down after repeated crashes", Scope: "server"},
	{ID: "install_finished", Label: "Install finished", Scope: "server"},
	{ID: "install_failed", Label: "Install failed", Scope: "server"},
	{ID: "backup_failed", Label: "Backup failed", Scope: "server"},
	{ID: "restore_finished", Label: "Restore finished or failed", Scope: "server"},
	{ID: "server_disk_full", Label: "Server over its disk limit", Scope: "server"},
	{ID: "cpu_high", Label: "CPU above", Scope: "server", Threshold: true, Unit: "%", Resolves: true},
	{ID: "memory_high", Label: "Memory above", Scope: "server", Threshold: true, Unit: "%", Resolves: true},
	{ID: "disk_high", Label: "Disk above", Scope: "server", Threshold: true, Unit: "%", Resolves: true},
	{ID: "node_offline", Label: "Node offline", Scope: "node", Resolves: true},
	{ID: "node_disk_low", Label: "Node's disk low", Scope: "node", Resolves: true},
	{ID: "node_cpu_high", Label: "Node CPU above", Scope: "node", Threshold: true, Unit: "%", Resolves: true},
	{ID: "node_memory_high", Label: "Node memory above", Scope: "node", Threshold: true, Unit: "%", Resolves: true},
	{ID: "node_disk_high", Label: "Node disk above", Scope: "node", Threshold: true, Unit: "%", Resolves: true},
	{ID: "wings_update", Label: "Wings updated or rolled back", Scope: "node"},
	{ID: "security", Label: "Signed actions and passkey changes", Scope: "node"},
}

// ConditionByID finds a condition.
func ConditionByID(id string) (Condition, bool) {
	i := slices.IndexFunc(Conditions, func(c Condition) bool { return c.ID == id })
	if i < 0 {
		return Condition{}, false
	}
	return Conditions[i], true
}

// ThresholdConditions are the conditions Wings evaluates.
func ThresholdConditions() []string {
	var out []string
	for _, c := range Conditions {
		if c.Threshold {
			out = append(out, c.ID)
		}
	}
	return out
}

// Message is one alert, as stored with each delivery and sent.
type Message struct {
	Condition string         `json:"condition"`
	Level     string         `json:"level"`
	Type      string         `json:"type"` // the event, or "node.offline"
	Title     string         `json:"title"`
	Text      string         `json:"text,omitempty"`
	Resolved  bool           `json:"resolved,omitempty"`
	Org       string         `json:"org,omitempty"`
	Node      string         `json:"node,omitempty"`
	NodeID    string         `json:"node_id,omitempty"`
	Server    string         `json:"server_name,omitempty"`
	ServerID  string         `json:"server_id,omitempty"`
	Rule      string         `json:"rule,omitempty"`
	URL       string         `json:"url,omitempty"` // where to look, in the app
	At        time.Time      `json:"at"`
	Data      map[string]any `json:"data,omitempty"`
}

// Event is a node event, as the mirror reads it.
type Event struct {
	Type     string
	ServerID string
	At       time.Time
	Data     map[string]any
}

func str(d map[string]any, k string) string {
	if v, ok := d[k]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

func num(d map[string]any, k string) float64 {
	switch v := d[k].(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

// bytes formats a size the way Wings does.
func size(n float64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%.0f B", n)
	}
	div, exp := float64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", n/div, "KMGTPE"[exp])
}

// FromEvent turns a node event into an alert for a condition. ok is false
// for events nothing alerts on. Threshold alerts (alert.fired and
// alert.resolved) name their rule in RuleID.
func FromEvent(e Event) (m Message, ruleID string, ok bool) {
	m = Message{Type: e.Type, ServerID: e.ServerID, At: e.At, Data: e.Data, Level: Warning}
	d := e.Data
	switch e.Type {
	case "server.crashed":
		m.Condition, m.Title = "crash", "Server crashed"
		m.Text = fmt.Sprintf("Exit code %.0f (%s).", num(d, "exit_code"), str(d, "reason"))
		if c, ok := d["console"].([]any); ok && len(c) > 0 {
			lines := make([]string, 0, len(c))
			for _, l := range c[max(0, len(c)-10):] {
				lines = append(lines, fmt.Sprint(l))
			}
			m.Text += "\nLast lines:\n" + strings.Join(lines, "\n")
		}
		m.Data = nil // the console lines are in the text
	case "server.crash_loop":
		m.Condition, m.Level, m.Title = "crash_loop", Critical, "Server down after repeated crashes"
		m.Text = fmt.Sprintf("It crashed %.0f times in a row and won't be restarted until someone starts it.", num(d, "crashes"))
	case "server.install.finished":
		m.Condition, m.Level, m.Title = "install_finished", Info, "Install finished"
		m.Text = "The server is installed and ready to start."
	case "server.install.failed":
		m.Condition, m.Title = "install_failed", "Install failed"
		m.Text = str(d, "error")
	case "server.delete.failed":
		m.Condition, m.Title = "backup_failed", "Server not deleted: its final backup failed"
		m.Text = str(d, "error")
	case "backup.finished":
		if str(d, "status") != "failed" {
			return m, "", false
		}
		m.Condition, m.Title = "backup_failed", "Backup failed"
		m.Text = str(d, "error")
	case "backup.restore.finished":
		m.Condition = "restore_finished"
		if ok, _ := d["ok"].(bool); ok {
			m.Level, m.Title = Info, "Backup restored"
		} else {
			m.Title, m.Text = "Restore failed", str(d, "error")
		}
	case "server.disk_limit_exceeded":
		m.Condition, m.Title = "server_disk_full", "Server over its disk limit"
		m.Text = fmt.Sprintf("Using %s of %s. It's stopped if it's still over at the next check.", size(num(d, "used_bytes")), size(num(d, "limit_bytes")))
	case "node.disk_low":
		m.Condition, m.Level, m.Title = "node_disk_low", Critical, "Node's disk low"
		m.Text = fmt.Sprintf("Below %s free: installs and image pulls are refused until space is freed.", size(num(d, "min_free")))
	case "node.disk_ok":
		m.Condition, m.Level, m.Resolved, m.Title = "node_disk_low", Info, true, "Node's disk has enough free space again"
	case "node.state_restored":
		m.Condition, m.Level, m.Title = "node_disk_low", Critical, "Wings' state database was corrupt and was restored from a snapshot"
		m.Text = fmt.Sprintf("Restored %s; changes after it were lost (at most an hour). The damaged copy is at %s. Check the disk's health.", str(d, "snapshot"), str(d, "corrupt_copy"))
	case "node.update":
		m.Condition, m.Level = "wings_update", Info
		from, to := str(d, "from"), str(d, "to")
		if str(d, "result") == "succeeded" {
			m.Title, m.Text = "Wings updated to "+to, "From "+from+"."
		} else {
			m.Level, m.Title = Warning, "Wings update to "+to+" rolled back"
			m.Text = fmt.Sprintf("Still on %s: %s", from, str(d, "error"))
		}
	case "security.audit":
		m.Condition = "security"
		securityText(&m, d)
	case "alert.fired", "alert.resolved":
		c, ok := ConditionByID(str(d, "condition"))
		if !ok {
			return m, "", false
		}
		m.Condition = c.ID
		value, limit := num(d, "value"), num(d, "threshold")
		if e.Type == "alert.fired" {
			m.Title = fmt.Sprintf("%s %.0f%s", c.Label, limit, c.Unit)
			m.Text = fmt.Sprintf("At %.0f%s for %s.", value, c.Unit, time.Duration(num(d, "for_seconds"))*time.Second)
		} else {
			m.Level, m.Resolved = Info, true
			m.Title = fmt.Sprintf("Back under: %s %.0f%s", strings.ToLower(c.Label[:1])+c.Label[1:], limit, c.Unit)
			m.Text = fmt.Sprintf("Now at %.0f%s.", value, c.Unit)
		}
		return m, str(d, "rule_id"), true
	default:
		return m, "", false
	}
	return m, "", true
}

// securityText describes a signed action, a pairing, or a key reset from
// the node's audit log.
func securityText(m *Message, d map[string]any) {
	m.Level = Warning
	action, outcome, detail := str(d, "action"), str(d, "outcome"), str(d, "detail")
	who := str(d, "user")
	if who == "" {
		who = str(d, "user_id")
	}
	if k := strings.TrimSpace(str(d, "key_name") + " " + str(d, "fingerprint")); k != "" {
		who += " with key " + k
	}
	switch {
	case action == "keys.reset":
		m.Level, m.Title = Critical, "Trusted passkeys reset on the node"
		m.Text = fmt.Sprintf("By %s: %s.", who, detail)
	case outcome == "rejected":
		m.Level, m.Title = Critical, "Signed action rejected: "+action
		m.Text = fmt.Sprintf("Requested by %s. Rejected: %s. If nobody you know did this, someone may be sending commands through the Panel.", who, detail)
	case outcome == "failed":
		m.Title = "Signed action failed: " + action
		m.Text = fmt.Sprintf("By %s: %s", who, detail)
	default:
		m.Title = "Signed action: " + action
		m.Text = "By " + who + "."
		if detail != "" {
			m.Text += " " + detail
		}
	}
}
