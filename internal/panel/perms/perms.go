// Package perms maps Wings' actions to what a user needs to run them
// (docs/PANEL.md#permissions). Owners and admins can run every action on
// their org's nodes; members only what their server grants allow. Anything
// not listed here is refused: a new Wings action does nothing for users
// until it's given a permission.
package perms

// Server permissions, granted to members per server.
const (
	ConsoleWrite = "console.write"
	Power        = "power"
	FilesRead    = "files.read"
	FilesWrite   = "files.write"
	Backups      = "backups"
	Schedules    = "schedules"
	Startup      = "startup"
	Reinstall    = "reinstall"
	SFTP         = "sftp"
)

// All is every server permission (the database checks the same list).
var All = []string{ConsoleWrite, Power, FilesRead, FilesWrite, Backups, Schedules, Startup, Reinstall, SFTP}

// adminOnly marks actions only admins and owners may run.
const adminOnly = ""

var actions = map[string]string{
	"server.command": ConsoleWrite,

	"server.start":   Power,
	"server.stop":    Power,
	"server.restart": Power,
	"server.kill":    Power,

	"files.list":     FilesRead,
	"files.stat":     FilesRead,
	"files.read":     FilesRead,
	"files.download": FilesRead,

	"files.write":         FilesWrite,
	"files.mkdir":         FilesWrite,
	"files.rename":        FilesWrite,
	"files.copy":          FilesWrite,
	"files.delete":        FilesWrite,
	"files.chmod":         FilesWrite,
	"files.compress":      FilesWrite,
	"files.decompress":    FilesWrite,
	"files.upload":        FilesWrite,
	"files.upload.status": FilesWrite,
	"files.upload.cancel": FilesWrite,

	// Deleting, unlocking, and restoring backups are passkey-signed too,
	// and a member's key needs an owner's delegation for them (Wings checks).
	"backup.create":  Backups,
	"backup.restore": Backups,
	"backup.lock":    Backups,
	"backup.delete":  Backups,

	"schedule.create": Schedules,
	"schedule.update": Schedules,
	"schedule.delete": Schedules,
	"schedule.run":    Schedules,

	"server.update":    Startup,
	"server.reinstall": Reinstall,

	"server.create":             adminOnly,
	"server.delete":             adminOnly,
	"backup.policy.update":      adminOnly,
	"backup.destination.save":   adminOnly,
	"backup.destination.delete": adminOnly,
	"node.sftp":                 adminOnly,
	"node.update":               adminOnly,
	"keys.add":                  adminOnly,
	"keys.remove":               adminOnly,
	"keys.pair":                 adminOnly,
}

// For returns what running action needs: a server permission, or
// adminOnly ("") for org admins and owners only. ok is false for actions
// users can't send at all.
func For(action string) (perm string, ok bool) {
	perm, ok = actions[action]
	return perm, ok
}

// AdminOnly reports whether only admins and owners may run action.
func AdminOnly(action string) bool {
	p, ok := actions[action]
	return ok && p == adminOnly
}

// Valid reports whether p is a server permission.
func Valid(p string) bool {
	for _, a := range All {
		if a == p {
			return true
		}
	}
	return false
}

// Reads are actions that change nothing, so they aren't written to the
// audit log.
func Read(action string) bool {
	switch action {
	case "files.list", "files.stat", "files.read", "files.download", "files.upload.status":
		return true
	}
	return false
}
