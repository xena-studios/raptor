package nodecmd

// Support access levels (docs/PANEL.md#support-access): what an owner can
// let Raptor's staff do on a node. Shared so the Panel refuses what Wings
// would, before sending it, and they never disagree.
const (
	SupportDiagnostics = 1 // look: health, checks, metrics, Wings' log
	SupportOperate     = 2 // and power servers and use their consoles
	SupportManage      = 3 // and files, schedules, backups
)

var supportLevels = map[string]int{
	"node.health": SupportDiagnostics, "node.doctor": SupportDiagnostics, "node.metrics": SupportDiagnostics,
	"node.disks": SupportDiagnostics, "node.system": SupportDiagnostics, "node.logs": SupportDiagnostics,
	"server.metrics": SupportDiagnostics, "server.ports": SupportDiagnostics,
	"backup.destinations": SupportDiagnostics, "backup.activity": SupportDiagnostics, "keys.list": SupportDiagnostics,

	"server.start": SupportOperate, "server.stop": SupportOperate, "server.restart": SupportOperate, "server.kill": SupportOperate,
	"server.console": SupportOperate, "server.command": SupportOperate,

	"files.list": SupportManage, "files.stat": SupportManage, "files.read": SupportManage, "files.write": SupportManage,
	"files.mkdir": SupportManage, "files.rename": SupportManage, "files.copy": SupportManage, "files.delete": SupportManage,
	"files.trash": SupportManage, "files.trash.restore": SupportManage,
	"addons.list": SupportManage, "addons.install": SupportManage,
	"schedule.create": SupportManage, "schedule.update": SupportManage, "schedule.delete": SupportManage, "schedule.run": SupportManage,
	"backup.create": SupportManage,
}

// SupportLevel returns the lowest support level that allows action, or 0
// if staff may never use it (anything passkey-signed, and anything that
// changes who has access).
func SupportLevel(action string) int { return supportLevels[action] }
