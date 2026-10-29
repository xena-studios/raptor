package actions

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/command"
)

// signing is which of Wings' actions need the user's passkey
// (docs/SECURITY-MODEL.md#passkey-signed-commands): always, never, or
// depending on the command (each of those has its own tests). A new action
// fails this test until it's added here, so whether it's signed is decided,
// not defaulted.
var signing = map[string]string{
	"addons.install": "never", "addons.list": "never",

	"backup.activity": "never", "backup.browse": "never", "backup.read": "never", "backup.create": "never",
	"backup.delete": "always", "backup.destination.delete": "always",
	"backup.destination.save": "depends", // adding or changing one; not Raptor Backup Storage
	"backup.destination.test": "never", "backup.destinations": "never", "backup.extract": "never",
	"backup.hostkey": "never", "backup.key": "never",
	"backup.key.mode": "depends", // handing the key to Raptor; not taking it back
	"backup.key.show": "always",
	"backup.lock":     "depends", // unlocking
	"backup.orphans":  "never", "backup.policy": "never",
	"backup.policy.update": "depends", // keeping fewer, or a new destination
	"backup.recover":       "always", "backup.restore": "always",

	"db.create": "never", "db.delete": "always", "db.list": "never", "db.password": "always", "db.password.reset": "never",
	"db.process.kill": "never", "db.processes": "never", "db.query": "never", "db.query.read": "never", "db.query.cancel": "never",
	"db.export": "never", "db.import": "never", "db.readonly.remove": "never", "db.keys": "never", "db.key": "never", "db.key.edit": "never",
	"db.redis.command": "never", "db.redis.command.read": "never", "db.collections": "never", "db.documents": "never",
	"db.document.edit": "never", "db.collection.edit": "never", "db.readonly.set": "never", "db.restore": "always",
	"db.transfer.cancel": "never", "db.transfer.status": "never",
	"db.row.delete": "never", "db.row.insert": "never", "db.row.update": "never", "db.rows": "never", "db.schema": "never",

	"files.chmod": "never", "files.compress": "never", "files.copy": "never", "files.decompress": "never",
	"files.delete": "never", "files.download": "never", "files.list": "never", "files.mkdir": "never",
	"files.read": "never", "files.rename": "never", "files.stat": "never", "files.trash": "never",
	"files.trash.delete": "never", "files.trash.empty": "never", "files.trash.restore": "never",
	"files.import": "never", "files.import.cancel": "never", "files.import.hostkey": "never", "files.import.status": "never",
	"files.pull": "never", "files.pull.cancel": "never", "files.pull.status": "never",
	"files.upload": "never", "files.upload.cancel": "never", "files.upload.status": "never", "files.write": "never",

	"keys.add": "always", "keys.list": "never", "keys.pair": "never", "keys.remove": "always",

	"node.disk.default": "never", "node.disk.grow": "never", "node.disk.setup": "always", "node.disks": "never",
	"node.doctor": "never", "node.firewall": "always", "node.health": "never", "node.hostname": "never",
	"node.logs": "never", "node.metrics": "never", "node.reboot": "always", "node.sftp": "never",
	"node.db.instance.delete": "always", "node.db.instance.external": "always", "node.db.instance.goback": "always", "node.db.instance.version": "never", "node.db.instance.power": "never", "node.db.instance.save": "never", "node.db.instances": "never", "node.db.usage": "never",
	"node.db.databases": "never", "node.db.users": "never", "node.db.processes": "never", "node.db.process.kill": "never",
	"node.db.database.create": "never", "node.db.database.delete": "always", "node.db.database.link": "never", "node.db.database.unlink": "never",
	"node.db.database.password": "always", "node.db.database.password.reset": "never",
	"node.db.backups": "never", "node.db.backup.create": "never", "node.db.backup.delete": "always", "node.db.backup.restore": "always",
	"node.db.backup.settings": "depends", // keeping fewer, or a new destination
	"node.network.delete":     "never", "node.network.rules": "never", "node.network.save": "never", "node.networks": "never",
	"node.system": "never", "node.time": "never",
	"node.update": "never", // the release is checked against Raptor's signing key instead

	"schedule.create": "never", "schedule.delete": "never", "schedule.run": "never", "schedule.update": "never",

	"server.command": "never", "server.create": "always", "server.delete": "always", "server.kill": "never",
	"server.metrics": "never", "server.move": "never", "server.networks": "never", "server.ports": "never",
	"server.reinstall": "depends", // wiping
	"server.restart":   "never", "server.start": "never", "server.stop": "never",
	"server.update": "depends", // the egg, install script, startup command, or image

	"sftp.disconnect": "never",

	"support.disable": "never", "support.grant": "always", "support.list": "never", "support.revoke": "never",
}

func TestSigningPolicy(t *testing.T) {
	x := &command.Executor{}
	Register(x, nil, Defaults{})
	RegisterSchedules(x, nil)
	RegisterBackups(x, nil)
	RegisterSFTP(x, nil)
	RegisterFiles(x, nil)
	RegisterAddons(x, nil)
	RegisterMetrics(x, nil, nil)
	RegisterNodeMetrics(x, nil, nil, nil)
	RegisterHealth(x, &Health{})
	RegisterSupport(x, nil)
	RegisterPorts(x, nil)
	RegisterUpdates(x, nil, UpdatePolicy{})
	RegisterDisks(x, nil)
	RegisterSystem(x, nil)
	RegisterNetworks(x, nil, nil)
	RegisterDatabases(x, nil)
	RegisterDatabaseBrowser(x, nil)
	RegisterNodeDatabases(x, nil)
	RegisterDatabaseTransfers(x, nil)
	RegisterDatabaseVersions(x, nil)
	RegisterDatabaseExternal(x, nil)
	RegisterDatabaseDocuments(x, nil)
	RegisterDatabaseBackups(x, nil, nil)

	same := func(a, b any) bool { return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer() }
	handlers := x.Handlers()
	for _, action := range slices.Sorted(maps.Keys(handlers)) {
		got := "depends"
		switch h := handlers[action]; {
		case same(h.Signed, command.Always):
			got = "always"
		case same(h.Signed, command.Never):
			got = "never"
		}
		want, ok := signing[action]
		switch {
		case !ok:
			t.Errorf("%s isn't in the signing table (it's %s)", action, got)
		case got != want:
			t.Errorf("%s: signed %s, want %s", action, got, want)
		}
	}
	for action := range signing {
		if _, ok := handlers[action]; !ok {
			t.Errorf("%s is in the signing table but isn't an action", action)
		}
	}
}
