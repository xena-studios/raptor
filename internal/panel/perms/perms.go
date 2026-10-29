// Package perms maps Wings' actions to what a user needs to run them
// (docs/PANEL.md#permissions). Owners and admins can run every action on
// their org's nodes; members only what their server grants allow. Anything
// not listed here is refused: a new Wings action does nothing for users
// until it's given a permission.
package perms

// Server permissions, granted to members per server.
const (
	// ConsoleRead is watching the console (its output can name players and
	// their addresses); ConsoleWrite, sending commands, includes it.
	ConsoleRead  = "console.read"
	ConsoleWrite = "console.write"
	Power        = "power"
	FilesRead    = "files.read"
	FilesWrite   = "files.write"
	Backups      = "backups"
	Schedules    = "schedules"
	Startup      = "startup"
	Reinstall    = "reinstall"
	SFTP         = "sftp"
	// DatabasesRead is seeing a server's databases and their connection
	// details (not passwords); DatabasesWrite, everything else on them,
	// includes it.
	DatabasesRead  = "databases.read"
	DatabasesWrite = "databases.write"
)

// All is every server permission (the database checks the same list).
var All = []string{ConsoleRead, ConsoleWrite, Power, FilesRead, FilesWrite, Backups, Schedules, Startup, Reinstall, SFTP, DatabasesRead, DatabasesWrite}

// adminOnly marks actions only admins and owners may run.
const adminOnly = ""

// View is any access to a server at all: actions that only show it (its
// resource graphs) need no particular permission. It isn't grantable.
const View = "view"

var actions = map[string]string{
	"server.metrics": View,
	"server.ports":   View,
	"server.console": ConsoleRead,
	"server.command": ConsoleWrite,

	"server.start":   Power,
	"server.stop":    Power,
	"server.restart": Power,
	"server.kill":    Power,

	"files.list":     FilesRead,
	"addons.list":    FilesRead,
	"files.stat":     FilesRead,
	"files.read":     FilesRead,
	"files.download": FilesRead,

	"files.write":         FilesWrite,
	"addons.install":      FilesWrite, // a plugin or mod into the egg's folder (docs/EGGS.md#add-ons)
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
	"files.pull":          FilesWrite, // a URL downloaded into the server's files
	"files.pull.status":   FilesWrite,
	"files.pull.cancel":   FilesWrite,
	// Importing adds files from another host and replaces none.
	"files.import":         FilesWrite,
	"files.import.hostkey": FilesWrite,
	"files.import.status":  FilesWrite,
	"files.import.cancel":  FilesWrite,
	// Restoring and emptying the trash change files; seeing it lists names
	// that were in the server.
	"files.trash":         FilesRead,
	"files.trash.restore": FilesWrite,
	"files.trash.delete":  FilesWrite,
	"files.trash.empty":   FilesWrite,

	// Deleting, unlocking, and restoring backups are passkey-signed too,
	// and a member's key needs an owner's delegation for them (Wings checks).
	"backup.create":  Backups,
	"backup.restore": Backups,
	"backup.lock":    Backups,
	"backup.delete":  Backups,
	// Pulling files out of a backup writes them into the server's .restore
	// folder, so it needs the backups permission like a restore.
	"backup.browse":   Backups,
	"backup.read":     Backups,
	"backup.extract":  Backups,
	"backup.activity": Backups,
	"backup.policy":   Backups,

	"schedule.create": Schedules,
	"schedule.update": Schedules,
	"schedule.delete": Schedules,
	"schedule.run":    Schedules,

	// A server's databases (docs/DATABASES.md). Showing a password is
	// passkey-signed; deleting a database too.
	"db.list":           DatabasesRead,
	"db.create":         DatabasesWrite,
	"db.delete":         DatabasesWrite,
	"db.password":       DatabasesWrite,
	"db.password.reset": DatabasesWrite,
	// The browser: looking is read-only queries in a read-only transaction;
	// changing is any SQL and editing rows.
	"db.schema":       DatabasesRead,
	"db.rows":         DatabasesRead,
	"db.query.read":   DatabasesRead,
	"db.processes":    DatabasesRead,
	"db.query":        DatabasesWrite,
	"db.row.insert":   DatabasesWrite,
	"db.row.update":   DatabasesWrite,
	"db.row.delete":   DatabasesWrite,
	"db.process.kill": DatabasesWrite,
	// Stopping a streamed query: only its own user's (Wings checks).
	"db.query.cancel": DatabasesRead,
	// Imports change data; exports and read-only users give it out.
	"db.import":          DatabasesWrite,
	"db.export":          DatabasesWrite,
	"db.transfer.status": DatabasesWrite,
	"db.transfer.cancel": DatabasesWrite,
	"db.readonly.set":    DatabasesWrite,
	"db.readonly.remove": DatabasesWrite,
	"db.restore":         DatabasesWrite,
	// The key and document browsers.
	"db.keys":               DatabasesRead,
	"db.key":                DatabasesRead,
	"db.redis.command.read": DatabasesRead,
	"db.collections":        DatabasesRead,
	"db.documents":          DatabasesRead,
	"db.key.edit":           DatabasesWrite,
	"db.redis.command":      DatabasesWrite,
	"db.document.edit":      DatabasesWrite,
	"db.collection.edit":    DatabasesWrite,

	"server.update":    Startup,
	"server.reinstall": Reinstall,

	"server.create":             adminOnly,
	"server.delete":             adminOnly,
	"server.move":               adminOnly,
	"backup.policy.update":      adminOnly,
	"backup.destination.save":   adminOnly,
	"backup.destination.delete": adminOnly,
	"backup.destination.test":   adminOnly,
	"backup.destinations":       adminOnly,
	"backup.hostkey":            adminOnly,
	"backup.key":                adminOnly,
	"backup.key.mode":           adminOnly,
	"backup.key.show":           adminOnly,
	"backup.recover":            adminOnly,
	"backup.orphans":            adminOnly,
	"node.sftp":                 adminOnly,
	"node.alerts":               adminOnly,
	"sftp.disconnect":           SFTP,
	"node.update":               adminOnly,
	"node.health":               adminOnly,
	"node.metrics":              adminOnly,
	"node.system":               adminOnly,
	"node.hostname":             adminOnly,
	"node.time":                 adminOnly,
	"node.firewall":             adminOnly,
	"node.reboot":               adminOnly,
	"node.disks":                adminOnly,
	"node.disk.setup":           adminOnly,
	"node.disk.default":         adminOnly,
	"node.disk.grow":            adminOnly,
	"node.logs":                 adminOnly,
	// Networks decide which servers can reach each other (docs/NETWORKS.md).
	"node.networks":       adminOnly,
	"node.network.save":   adminOnly,
	"node.network.delete": adminOnly,
	"node.network.rules":  adminOnly,
	// Database instances on the node.
	"node.db.instances":         adminOnly,
	"node.db.usage":             adminOnly,
	"node.db.instance.save":     adminOnly,
	"node.db.instance.power":    adminOnly,
	"node.db.instance.delete":   adminOnly,
	"node.db.instance.version":  adminOnly,
	"node.db.instance.goback":   adminOnly,
	"node.db.instance.external": adminOnly,
	"server.networks":           adminOnly,
	"support.grant":             adminOnly,
	"support.revoke":            adminOnly,
	"support.list":              adminOnly,
	"support.disable":           adminOnly,
	"node.doctor":               adminOnly,
	"keys.add":                  adminOnly,
	"keys.remove":               adminOnly,
	"keys.pair":                 adminOnly,
	"keys.list":                 adminOnly,

	// An instance's own page: its databases, users, and connections.
	"node.db.databases":    adminOnly,
	"node.db.users":        adminOnly,
	"node.db.processes":    adminOnly,
	"node.db.process.kill": adminOnly,

	// A node's databases on their own, and which servers use each.
	"node.db.database.create":         adminOnly,
	"node.db.database.delete":         adminOnly,
	"node.db.database.link":           adminOnly,
	"node.db.database.unlink":         adminOnly,
	"node.db.database.password":       adminOnly,
	"node.db.database.password.reset": adminOnly,
}

// For returns what running action needs: a server permission, or
// adminOnly ("") for org admins and owners only. ok is false for actions
// users can't send at all.
func For(action string) (perm string, ok bool) {
	perm, ok = actions[action]
	return perm, ok
}

// Allows reports whether a member with these server permissions has perm.
// console.write includes console.read: sending commands blind is useless;
// databases.write includes databases.read.
func Allows(granted []string, perm string) bool {
	if perm == View {
		return len(granted) > 0
	}
	for _, g := range granted {
		if g == perm || (perm == ConsoleRead && g == ConsoleWrite) || (perm == DatabasesRead && g == DatabasesWrite) {
			return true
		}
	}
	return false
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
	case "files.list", "addons.list", "files.stat", "files.trash", "files.read", "files.download", "files.upload.status", "keys.list",
		"backup.browse", "backup.read", "backup.activity", "server.metrics", "server.ports", "node.health", "node.doctor", "node.metrics", "node.system", "node.disks", "node.logs", "node.networks", "node.db.instances", "node.db.usage", "db.list", "db.schema", "db.rows", "db.query.read", "db.query.cancel", "db.processes", "node.db.databases", "node.db.users", "node.db.processes", "db.transfer.status", "db.keys", "db.key", "db.redis.command.read", "db.collections", "db.documents", "support.list",
		"backup.destinations", "backup.hostkey", "backup.policy", "backup.orphans":
		return true
	}
	return false
}
