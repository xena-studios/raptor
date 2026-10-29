package alerts

import (
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestFromEvent(t *testing.T) {
	at := time.Now()
	for _, c := range []struct {
		typ       string
		data      map[string]any
		condition string
		level     string
		title     string
		resolved  bool
	}{
		{"server.crashed", map[string]any{"exit_code": 1.0, "reason": "error"}, "crash", Warning, "Server crashed", false},
		{"server.crash_loop", map[string]any{"crashes": 5.0}, "crash_loop", Critical, "Server down after repeated crashes", false},
		{"server.install.finished", nil, "install_finished", Info, "Install finished", false},
		{"server.install.failed", map[string]any{"error": "x"}, "install_failed", Warning, "Install failed", false},
		{"backup.finished", map[string]any{"status": "failed", "error": "x"}, "backup_failed", Warning, "Backup failed", false},
		{"backup.restore.finished", map[string]any{"ok": true}, "restore_finished", Info, "Backup restored", false},
		{"server.disk_limit_exceeded", map[string]any{"used_bytes": 2048.0, "limit_bytes": 1024.0}, "server_disk_full", Warning, "Server over its disk limit", false},
		{"node.disk_low", map[string]any{"min_free": 1e9}, "node_disk_low", Critical, "Node's disk low", false},
		{"node.disk_ok", nil, "node_disk_low", Info, "Node's disk has enough free space again", true},
		{"node.update", map[string]any{"result": "succeeded", "from": "v1", "to": "v2"}, "wings_update", Info, "Wings updated to v2", false},
		{"node.update", map[string]any{"result": "rolled_back", "from": "v1", "to": "v2"}, "wings_update", Warning, "Wings update to v2 rolled back", false},
		{"security.audit", map[string]any{"action": "server.delete", "outcome": "rejected", "detail": "bad signature", "user": "Alice"}, "security", Critical, "Signed action rejected: server.delete", false},
		{"security.audit", map[string]any{"action": "keys.reset", "outcome": "ok", "user_id": "root"}, "security", Critical, "Trusted passkeys reset on the node", false},
		{"db.instance.down", map[string]any{"instance": "main", "engine": "postgres", "version": "17", "error": "it stopped (exit code 137)"}, "db_down", Critical, "Database instance down: main", false},
		{"db.instance.up", map[string]any{"instance": "main", "down_seconds": 42.0}, "db_down", Info, "Database instance back up: main", true},
		{"db.instance.version", map[string]any{"instance": "main", "kind": "version", "from": "16", "to": "17", "ok": true}, "db_version", Info, "Version change finished: main", false},
		{"db.instance.version", map[string]any{"instance": "main", "kind": "patch", "from": "17", "to": "17", "ok": false, "error": "x"}, "db_version", Warning, "Patch update failed: main", false},
		{"db.instance.version", map[string]any{"instance": "main", "kind": "back", "from": "17", "to": "16", "ok": true}, "db_version", Info, "Going back finished: main", false},
	} {
		m, rule, ok := FromEvent(Event{Type: c.typ, At: at, Data: c.data})
		if !ok || rule != "" || m.Condition != c.condition || m.Level != c.level || m.Title != c.title || m.Resolved != c.resolved {
			t.Errorf("%s %v: %+v (%v)", c.typ, c.data, m, ok)
		}
		if _, ok := ConditionByID(m.Condition); !ok {
			t.Errorf("%s: condition %q isn't one a rule can have", c.typ, m.Condition)
		}
	}
	for _, typ := range []string{"server.state", "server.console.command", "backup.finished"} {
		if _, _, ok := FromEvent(Event{Type: typ, Data: map[string]any{"status": "ok"}}); ok {
			t.Errorf("%s alerted", typ)
		}
	}
	m, rule, ok := FromEvent(Event{Type: "alert.fired", Data: map[string]any{"rule_id": "r1", "condition": "node_memory_high", "value": 93.4, "threshold": 90.0, "for_seconds": 600.0}})
	if !ok || rule != "r1" || m.Title != "Node memory above 90%" || m.Text != "At 93% for 10m0s." {
		t.Errorf("fired: %+v %q", m, rule)
	}
	if _, _, ok := FromEvent(Event{Type: "alert.fired", Data: map[string]any{"condition": "nope"}}); ok {
		t.Error("an unknown threshold condition alerted")
	}
	m, rule, ok = FromEvent(Event{Type: "alert.fired", Data: map[string]any{
		"rule_id": "r2", "condition": "db_connections_high", "value": 95.0, "threshold": 90.0, "for_seconds": 300.0, "instance": "main",
	}})
	if !ok || rule != "r2" || m.Title != "Database instance connections above 90%: main" || !strings.HasPrefix(m.Text, "At 95% for 5m0s. Of its max connections") {
		t.Errorf("instance fired: %+v %q", m, rule)
	}
	m, _, _ = FromEvent(Event{Type: "alert.resolved", Data: map[string]any{
		"rule_id": "r3", "condition": "db_disk_high", "value": 70.0, "threshold": 90.0, "instance": "main",
	}})
	if !m.Resolved || m.Title != "Back under: database instance disk above 90%: main" {
		t.Errorf("instance resolved: %+v", m)
	}
	m, _, _ = FromEvent(Event{Type: "db.instance.down", Data: map[string]any{"instance": "main", "engine": "postgres", "version": "17", "error": "it stopped (exit code 137)"}})
	if !strings.HasPrefix(m.Text, "main (PostgreSQL 17): it stopped (exit code 137).") {
		t.Errorf("down: %q", m.Text)
	}
	// An instance's own backup: only a failed one alerts.
	if _, _, ok := FromEvent(Event{Type: "db.backup.finished", Data: map[string]any{"instance": "main", "status": "ok"}}); ok {
		t.Error("a finished instance backup alerted")
	}
	m, _, ok = FromEvent(Event{Type: "db.backup.finished", Data: map[string]any{"instance": "main", "engine": "postgres", "status": "failed", "error": "dumping app: the instance isn't running"}})
	if !ok || m.Condition != "db_backup_failed" || m.Title != "Database backup failed: main" || !strings.Contains(m.Text, "isn't running") {
		t.Errorf("failed instance backup: %+v", m)
	}
}

// Instance conditions are kept from nodes that don't know them.
func TestInstanceConditions(t *testing.T) {
	got := InstanceConditions()
	if len(got) != 5 {
		t.Errorf("%v", got)
	}
	for _, id := range got {
		c, _ := ConditionByID(id)
		if c.Scope != "instance" {
			t.Errorf("%s: scope %q", id, c.Scope)
		}
	}
}

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"1.1.1.1": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.0.0.3": false, "192.168.1.1": false, "172.16.0.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "0.1.2.3": false, "::1": false, "fd00::1": false, "fe80::1": false,
		"::ffff:10.0.0.1": false, "224.0.0.1": false,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}

func TestPrepare(t *testing.T) {
	c, s, hint, err := prepare(ChannelInput{Type: Discord, URL: "https://ptb.discord.com/api/webhooks/123456789/secret-token"}, false)
	if err != nil || s.URL != "https://discord.com/api/webhooks/123456789/secret-token" || strings.Contains(hint, "secret") || c != (config{}) {
		t.Errorf("discord: %+v %+v %q %v", c, s, hint, err)
	}
	if _, _, _, err := prepare(ChannelInput{Type: Webhook, URL: "http://example.com/x"}, false); err == nil {
		t.Error("an http:// webhook was accepted")
	}
	if _, _, _, err := prepare(ChannelInput{Type: Webhook, URL: "https://user:pass@example.com/x"}, false); err == nil {
		t.Error("a URL with credentials was accepted")
	}
	c, s, _, err = prepare(ChannelInput{Type: Ntfy, NtfyTopic: "raptor-alerts_1"}, false)
	if err != nil || c.Server != DefaultNtfy || s.Topic != "raptor-alerts_1" {
		t.Errorf("ntfy: %+v %+v %v", c, s, err)
	}
	if _, _, _, err := prepare(ChannelInput{Type: Ntfy, NtfyServer: "https://ntfy.example/path", NtfyTopic: "x"}, false); err == nil {
		t.Error("an ntfy server with a path was accepted")
	}
}

func TestTelegramText(t *testing.T) {
	got := telegramText(Message{Level: Warning, Title: "a <b>", Text: "x & y", Server: "smp", Node: "box"})
	if !strings.Contains(got, "<b>a &lt;b&gt;</b>") || !strings.Contains(got, "x &amp; y") || !strings.Contains(got, "smp on box") {
		t.Errorf("%q", got)
	}
}
