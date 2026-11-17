package staff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/perms"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// demoUsage is `panel staff demo`'s help (docs/DEPLOY.md#public-demo).
const demoUsage = `usage: panel staff demo on|off <org-id>
       panel staff demo server <org-id> <node-id> <server-id> [permission,...]
       panel staff demo backup <node-id> <server-id> <backup-id>
       panel staff demo remove <node-id> <server-id>
       panel staff demo status`

// demoCLI is `panel staff demo`: making an org Raptor's public demo
// (#449), and choosing its showcase servers and what guests may do on
// them. Only staff on the Panel's server can: nothing a user calls sets
// orgs.demo. Each change is in the staff log.
func demoCLI(ctx context.Context, q *store.Queries, by string, out io.Writer, args []string) error {
	logged := func(action, targetType, target string, meta map[string]any) error {
		m, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		// Done from the server, not by a staff account: no one's ID.
		return q.AddStaffAudit(ctx, store.AddStaffAuditParams{
			StaffID: pgtype.UUID{Valid: true}, StaffEmail: by, Action: action, TargetType: targetType, TargetID: target, Meta: m,
		})
	}
	switch {
	case len(args) == 2 && (args[0] == "on" || args[0] == "off"):
		org, err := parseUUID(args[1], "org")
		if err != nil {
			return err
		}
		on := args[0] == "on"
		n, err := q.SetOrgDemo(ctx, store.SetOrgDemoParams{ID: org, Demo: on})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("no org %s", args[1])
		}
		if err := logged("org.demo."+args[0], "org", args[1], nil); err != nil {
			return err
		}
		if on {
			_, _ = fmt.Fprintln(out, "That org is the demo now: guests can start sessions in it, and its deny list applies to everyone but its owners.")
			_, _ = fmt.Fprintln(out, "Add its showcase servers with: panel staff demo server <org-id> <node-id> <server-id>")
		} else {
			_, _ = fmt.Fprintln(out, "That org isn't the demo any more. Guests already in it stay until their hour is up.")
		}
		return nil
	case (len(args) == 4 || len(args) == 5) && args[0] == "server":
		org, err := parseUUID(args[1], "org")
		if err != nil {
			return err
		}
		node, err := parseUUID(args[2], "node")
		if err != nil {
			return err
		}
		if owner, err := q.NodeOrg(ctx, node); err != nil || owner != org {
			return fmt.Errorf("node %s isn't in org %s", args[2], args[1])
		}
		list := slices.Clone(perms.DemoGuest)
		if len(args) == 5 {
			if list, err = demoPermissions(args[4]); err != nil {
				return err
			}
		}
		if err := q.SetDemoServer(ctx, store.SetDemoServerParams{OrgID: org, NodeID: node, ServerID: args[3], Permissions: list}); err != nil {
			return err
		}
		if err := logged("demo.server", "server", args[2]+"/"+args[3], map[string]any{"permissions": list}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "Guests who start from now on get %s on %s.\n", strings.Join(list, ", "), args[3])
		if ok, _ := q.MirroredServerExists(ctx, store.MirroredServerExistsParams{NodeID: node, ServerID: args[3]}); !ok {
			_, _ = fmt.Fprintln(out, "The Panel hasn't seen that server on the node yet: check the ID.")
		}
		return nil
	case len(args) == 4 && args[0] == "backup":
		node, err := parseUUID(args[1], "node")
		if err != nil {
			return err
		}
		row, err := demoServer(ctx, q, node, args[2])
		if err != nil {
			return err
		}
		row.KnownGoodBackup = strings.TrimSpace(args[3])
		if err := q.SetDemoServer(ctx, store.SetDemoServerParams{
			OrgID: row.OrgID, NodeID: row.NodeID, ServerID: row.ServerID, Permissions: row.Permissions, KnownGoodBackup: row.KnownGoodBackup,
		}); err != nil {
			return err
		}
		if err := logged("demo.backup", "server", args[1]+"/"+args[2], map[string]any{"backup": row.KnownGoodBackup}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s's known-good backup is %s. Lock it in the app so it's never pruned.\n", args[2], row.KnownGoodBackup)
		_, _ = fmt.Fprintln(out, "The hourly reset doesn't restore it yet: that needs the demo key (Phase 2, DECISIONS #375).")
		return nil
	case len(args) == 3 && args[0] == "remove":
		node, err := parseUUID(args[1], "node")
		if err != nil {
			return err
		}
		n, err := q.DeleteDemoServer(ctx, store.DeleteDemoServerParams{NodeID: node, ServerID: args[2]})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%s isn't a showcase server", args[2])
		}
		if err := logged("demo.server.remove", "server", args[1]+"/"+args[2], nil); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s isn't a showcase server any more: guests who start from now on don't get it.\n", args[2])
		return nil
	case len(args) == 1 && args[0] == "status":
		orgs, err := q.DemoOrgs(ctx)
		if err != nil {
			return err
		}
		if len(orgs) == 0 {
			_, _ = fmt.Fprintln(out, "No demo org: the demo isn't open. Make one with: panel staff demo on <org-id>")
			return nil
		}
		for _, o := range orgs {
			last := "never"
			if o.DemoResetAt.Valid {
				last = o.DemoResetAt.Time.UTC().Format("2006-01-02 15:04 UTC")
			}
			_, _ = fmt.Fprintf(out, "Demo org %s (last reset %s)\n", uuid.UUID(o.ID.Bytes), last)
			servers, err := q.DemoServers(ctx, o.ID)
			if err != nil {
				return err
			}
			for _, s := range servers {
				backup := s.KnownGoodBackup
				if backup == "" {
					backup = "no known-good backup"
				}
				_, _ = fmt.Fprintf(out, "  %s/%s: %s; %s\n", uuid.UUID(s.NodeID.Bytes), s.ServerID, strings.Join(s.Permissions, ", "), backup)
			}
			if len(servers) == 0 {
				_, _ = fmt.Fprintln(out, "  no showcase servers: guests see nothing")
			}
		}
		return nil
	}
	return errors.New(demoUsage)
}

// demoPermissions parses a comma-separated list of what guests may do on
// a showcase server.
func demoPermissions(s string) ([]string, error) {
	var list []string
	for p := range strings.SplitSeq(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !perms.DemoGrantable(p) {
			return nil, fmt.Errorf("guests can't be given %q in the demo", p)
		}
		if !slices.Contains(list, p) {
			list = append(list, p)
		}
	}
	if len(list) == 0 {
		return nil, errors.New("name at least one permission")
	}
	slices.Sort(list)
	return list, nil
}

func demoServer(ctx context.Context, q *store.Queries, node pgtype.UUID, server string) (store.DemoServer, error) {
	org, err := q.NodeOrg(ctx, node)
	if err != nil {
		return store.DemoServer{}, fmt.Errorf("no node %s", uuid.UUID(node.Bytes))
	}
	rows, err := q.DemoServers(ctx, org)
	if err != nil {
		return store.DemoServer{}, err
	}
	for _, r := range rows {
		if r.NodeID == node && r.ServerID == server {
			return r, nil
		}
	}
	return store.DemoServer{}, fmt.Errorf("%s isn't a showcase server: add it with panel staff demo server first", server)
}

func parseUUID(s, what string) (pgtype.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("bad %s ID %q", what, s)
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}
