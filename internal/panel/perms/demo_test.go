package perms

import (
	"slices"
	"testing"
)

// Each thing the demo turns off (#449) is refused, and what guests are
// there to try isn't.
func TestDemoRefuses(t *testing.T) {
	type cmd struct {
		action, params string
		signed         bool
	}
	off := map[string]cmd{
		"create a server":            {action: "server.create"},
		"delete a server":            {action: "server.delete"},
		"wipe and reinstall":         {action: "server.reinstall", params: `{"wipe":true}`},
		"reinstall":                  {action: "server.reinstall"},
		"change the egg":             {action: "server.update", params: `{"egg":"eyJ9"}`},
		"reinstall from settings":    {action: "server.update", params: `{"reinstall":{"wipe":false}}`},
		"change the image (signed)":  {action: "server.update", params: `{"image":"ghcr.io/x/y"}`, signed: true},
		"anything signed":            {action: "server.start", signed: true},
		"restore a backup":           {action: "backup.restore", params: `{"backup_id":"b1"}`},
		"delete a backup":            {action: "backup.delete"},
		"lock a backup":              {action: "backup.lock"},
		"recover backups":            {action: "backup.recover"},
		"backup retention":           {action: "backup.policy.update"},
		"backup destinations":        {action: "backup.destination.save"},
		"backup key":                 {action: "backup.key.show"},
		"upload from a link":         {action: "files.pull", params: `{"url":"https://example.com/x"}`},
		"upload from a link: status": {action: "files.pull.status"},
		"SFTP importer":              {action: "files.import"},
		"SFTP importer: host key":    {action: "files.import.hostkey"},
		"SFTP":                       {action: "sftp.disconnect"},
		"node SFTP":                  {action: "node.sftp"},
		"external database access":   {action: "node.db.instance.external"},
		"database password":          {action: "db.password"},
		"database read-only user":    {action: "db.readonly.set"},
		"networks":                   {action: "node.network.save"},
		"server networks":            {action: "server.networks"},
		"firewall":                   {action: "node.firewall"},
		"node settings":              {action: "node.hostname"},
		"node update":                {action: "node.update"},
		"reboot":                     {action: "node.reboot"},
		"support access":             {action: "support.grant"},
		"trusted passkeys":           {action: "keys.add"},
		"move a server":              {action: "server.move"},
		"unreadable update":          {action: "server.update", params: `{"egg":`},
	}
	for name, c := range off {
		if !DemoRefuses(c.action, []byte(c.params), c.signed) {
			t.Errorf("%s (%s) is allowed in the demo", name, c.action)
		}
		if _, ok := For(c.action); !ok && c.action != "server.update" {
			t.Errorf("%s: %s isn't an action", name, c.action)
		}
	}
	on := map[string]cmd{
		"watch the console":   {action: "server.console"},
		"send a command":      {action: "server.command", params: `{"command":"say hi"}`},
		"start":               {action: "server.start"},
		"browse files":        {action: "files.list"},
		"edit a file":         {action: "files.write"},
		"upload a file":       {action: "files.upload"},
		"empty the trash":     {action: "files.trash.empty"},
		"install an add-on":   {action: "addons.install"},
		"run a schedule":      {action: "schedule.run"},
		"make a schedule":     {action: "schedule.create"},
		"take a backup":       {action: "backup.create"},
		"browse a backup":     {action: "backup.browse"},
		"change memory":       {action: "server.update", params: `{"memory_mb":2048,"image":"ghcr.io/x/y","startup":"java"}`},
		"see the graphs":      {action: "server.metrics"},
		"players":             {action: "players.manage", params: `{"op":"kick"}`},
		"backup plan (reads)": {action: "backup.policy"},
	}
	for name, c := range on {
		if DemoRefuses(c.action, []byte(c.params), c.signed) {
			t.Errorf("%s (%s) is refused in the demo", name, c.action)
		}
	}
}

func TestDemoGuestGrants(t *testing.T) {
	for _, p := range DemoGuest {
		if !DemoGrantable(p) {
			t.Errorf("guests get %q, which isn't grantable in the demo", p)
		}
	}
	for _, p := range []string{SFTP, Reinstall, DatabasesRead, DatabasesWrite, "nope"} {
		if DemoGrantable(p) || slices.Contains(DemoGuest, p) {
			t.Errorf("%q is grantable in the demo", p)
		}
	}
}
