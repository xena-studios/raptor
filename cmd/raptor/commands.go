package main

import (
	"strings"
)

// cliCommand is one command people run. The usage text is made from this
// list, and tests check that every entry is routed by run and is on the docs
// site's reference page, and that the page names nothing else
// (commands_test.go), so the three can't drift apart.
type cliCommand struct {
	name    string // "backup create"
	args    string // "<server> [-lock]"
	root    bool   // needs root
	summary string
	// internal commands are run by systemd or Wings, not people: they're
	// in the usage, but not on the docs site.
	internal bool
}

var cliCommands = []cliCommand{
	{name: "status", summary: "the node's health: its link to the Panel, Docker, storage, and servers"},
	{name: "ps", args: "[-json]", summary: "servers with their state and resource usage"},
	{name: "start", args: "<server>", root: true, summary: "start a server"},
	{name: "stop", args: "<server>", root: true, summary: "stop a server, waiting for a clean shutdown"},
	{name: "restart", args: "<server>", root: true, summary: "stop a server, then start it"},
	{name: "kill", args: "<server>", root: true, summary: "stop a server at once, without saving"},
	{name: "console", args: "<server>", root: true, summary: "the live console; type commands to send them"},
	{name: "logs", args: "<server> [-n lines] [-f] [-t]", summary: "server output, further back than the console history"},
	{name: "tui", summary: "a terminal view of the servers: stats, console, and keys for power, backups, and commands (actions as root)"},
	{name: "audit", args: "[-n N] [-since 72h] [-json]", summary: "signed actions and key resets, from the node's own records"},
	{name: "backup list", args: "[<server>] [-json]", summary: "a server's backups, or every server's"},
	{name: "backup create", args: "<server> [-lock] [-no-wait]", root: true, summary: "back up now, and wait for it"},
	{name: "backup restore", args: "<server> <backup> [-yes] [-no-wait]", root: true, summary: "restore a backup, after a safety backup"},
	{name: "backup key", root: true, summary: "the node's backup key, to keep somewhere safe away from this machine"},
	{name: "storage status", summary: "the server data volume: its kind, size, and how full it is"},
	{name: "storage setup", args: "-size s", root: true, summary: "create the server data volume's image, if bootstrap didn't"},
	{name: "storage grow", args: "-size s", root: true, summary: "grow the volume's image while servers run"},
	{name: "storage disks", summary: "the machine's disks, and what each is used for"},
	{name: "storage add-disk", args: "-device /dev/sdX [-role servers|backups]", root: true, summary: "format an empty disk and mount it for servers or backups"},
	{name: "storage grow-disk", args: "-id id", root: true, summary: "grow a data disk's filesystem after its disk was made bigger"},
	{name: "doctor", args: "[-json] [-bundle] [-upload]", summary: "check the node and say how to fix what's wrong (as root); -upload sends a redacted diagnostics file to support for a support code"},
	{name: "update", args: "[-check] [-version v]", root: true, summary: "install the newest Wings in the node's channel, or a given version; servers keep running, and it rolls back if the new version isn't healthy"},
	{name: "keys list", args: "[-json]", summary: "the passkeys this node trusts for signed actions"},
	{name: "keys reset", root: true, summary: "re-pair the owner's passkey from the machine"},
	{name: "support status", summary: "whether Raptor's staff have access to this node, and until when"},
	{name: "support revoke", args: "<id>|all", root: true, summary: "end staff access now"},
	{name: "support off", root: true, summary: "refuse all staff access"},
	{name: "support on", root: true, summary: "allow staff access requests again"},
	{name: "import pterodactyl", args: "-key <ptla_…> [-server s] [-dry-run] [-start]", root: true, summary: "move servers from Pterodactyl on this machine; their files are copied, Pterodactyl's are left as they were"},
	{name: "bootstrap", args: "-token <rpt_join_…> [-storage image|disk|soft] [-size s] [-yes]", root: true, summary: "set up this machine as a node and link it: Docker, the raptor user, services, the server data volume"},
	{name: "link", args: "-token <rpt_join_…> [-panel url]", root: true, summary: "link this node to the Panel with a join token; Wings restarts and connects, servers keep running"},
	{name: "relink", args: "-token <rpt_join_…> [-node id]", root: true, summary: "link again as the same node, with a new key (its key was revoked, or it was removed or unlinked)"},
	{name: "unlink", args: "[-yes]", root: true, summary: "stop connecting to the Panel; servers keep running"},
	{name: "uninstall", args: "[-wipe-data] [-yes]", root: true, summary: "take Raptor off this machine: stop the servers gracefully and remove its services, containers, networks, firewall rules, mounts, and this command; server files, backups, and databases stay in /var/lib/raptor unless -wipe-data"},
	{name: "version", summary: "the Wings version"},
	{name: "wings run", internal: true, summary: "run the Wings daemon (raptor-wings.service)"},
	{name: "wings shutdown-servers", root: true, internal: true, summary: "stop every server gracefully for a host shutdown (raptor-shutdown.service); they start again at boot"},
}

// usage is the help printed for an unknown command.
var usage = makeUsage(cliCommands)

func makeUsage(cmds []cliCommand) string {
	var b strings.Builder
	b.WriteString("usage: raptor <command> [flags]\n\ncommands:\n")
	for _, c := range cmds {
		b.WriteString("  " + strings.TrimSpace(c.name+" "+c.args) + "\n")
		summary := c.summary
		if c.root {
			summary += " (root)"
		}
		for _, line := range wrap(summary, 66) {
			b.WriteString("              " + line + "\n")
		}
	}
	b.WriteString("\n<server> is a server's ID, the short ID from ps, or its exact name.\n")
	b.WriteString(`Run "raptor <command> -h" for a command's flags.`)
	return b.String()
}

// wrap breaks s into lines of at most width characters, at spaces.
func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
