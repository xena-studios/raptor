package perms

import (
	"encoding/json"
	"strings"
)

// The public demo's deny list for commands (#449,
// docs/SECURITY-MODEL.md#the-public-demo). In the demo org, the Panel
// refuses these to everyone but the org's owners (auth.DemoRestricted),
// whatever their grants say: hiding the buttons isn't enough.

// demoDeniedPrefixes are whole families of actions that are off.
var demoDeniedPrefixes = []string{
	// The node's settings, networks and firewall, disks, databases
	// instances (and their external access), and updates.
	"node.",
	// Support access, and the node's trusted passkeys.
	"support.", "keys.",
	// Upload from a link and the SFTP importer reach out to other hosts.
	"files.pull", "files.import",
	// SFTP.
	"sftp.",
	// Where backups go and their keys.
	"backup.destination", "backup.key",
}

// demoDenied are single actions that are off.
var demoDenied = map[string]bool{
	// Passkey-signed in any Raptor: creating and deleting servers, wipe
	// reinstalls, restores over files. Moving a server isn't signed, but
	// it's a node setting.
	"server.create":    true,
	"server.delete":    true,
	"server.move":      true,
	"server.reinstall": true,
	"server.networks":  true,
	"backup.restore":   true,
	"backup.delete":    true,
	"backup.lock":      true,
	"backup.recover":   true,
	"backup.orphans":   true,
	"backup.hostkey":   true,
	// Retention and where a server's backups go.
	"backup.policy.update": true,
	// Database passwords and read-only users are ways in from outside.
	"db.password":        true,
	"db.password.reset":  true,
	"db.readonly.set":    true,
	"db.readonly.remove": true,
}

// DemoRefuses reports whether the demo's deny list refuses a command:
// anything passkey-signed (signed), and the actions above. server.update
// is allowed (memory, CPU, variables) unless it changes the egg or
// reinstalls; a change of image or startup command needs a passkey, so
// it comes signed and is refused for that, or Wings refuses it unsigned.
func DemoRefuses(action string, params json.RawMessage, signed bool) bool {
	if signed || demoDenied[action] {
		return true
	}
	for _, p := range demoDeniedPrefixes {
		if strings.HasPrefix(action, p) {
			return true
		}
	}
	if action == "server.update" {
		var p struct {
			Egg       json.RawMessage `json:"egg"`
			Reinstall json.RawMessage `json:"reinstall"`
		}
		if len(params) > 0 && json.Unmarshal(params, &p) != nil {
			return true // the node couldn't read it either
		}
		return present(p.Egg) || present(p.Reinstall)
	}
	return false
}

// present reports whether a JSON field was given a value.
func present(v json.RawMessage) bool {
	s := strings.TrimSpace(string(v))
	return s != "" && s != "null" && s != `""`
}

// DemoGuest is what guests get on a showcase server when staff don't say
// otherwise: the console, power, files and add-ons, backups, schedules,
// settings like memory, and players. Never SFTP, reinstalls, or databases.
var DemoGuest = []string{ConsoleWrite, Power, FilesRead, FilesWrite, Backups, Schedules, Startup, Players}

// DemoGrantable reports whether a guest may be granted p on a showcase
// server: what's on the deny list anyway isn't.
func DemoGrantable(p string) bool {
	switch p {
	case SFTP, Reinstall, DatabasesRead, DatabasesWrite:
		return false
	}
	return Valid(p)
}
