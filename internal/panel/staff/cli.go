package staff

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// CLI is `panel staff add|remove|list`, run on the Panel's server: the only
// way anyone becomes staff. Granting is recorded in the staff log, as done
// by the server's own user.
func CLI(ctx context.Context, db *pgxpool.Pool, out io.Writer, args []string) error {
	q := store.New(db)
	by := "cli"
	if u, err := user.Current(); err == nil {
		by = "cli:" + u.Username
	}
	if su := os.Getenv("SUDO_USER"); su != "" {
		by = "cli:" + su
	}
	switch {
	case len(args) == 3 && args[0] == "add":
		role := args[2]
		if role != Admin && role != Support {
			return errors.New(`the role is "admin" or "support"`)
		}
		u, err := q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(args[1])))
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("no account uses %s: they sign up first", args[1])
		}
		if err != nil {
			return err
		}
		if err := q.AddStaff(ctx, store.AddStaffParams{UserID: u.ID, Role: role, AddedBy: by}); err != nil {
			return err
		}
		if err := q.AddStaffAudit(ctx, store.AddStaffAuditParams{
			StaffID: u.ID, StaffEmail: by, Action: "staff.add", TargetType: "user", TargetID: id(u.ID),
			Meta: []byte(fmt.Sprintf(`{"role":%q,"email":%q}`, role, u.Email)),
		}); err != nil {
			return err
		}
		n, _ := q.CountPasskeys(ctx, u.ID)
		_, _ = fmt.Fprintf(out, "%s is %s staff now.\n", u.Email, role)
		if n == 0 {
			_, _ = fmt.Fprintln(out, "They have no passkey yet: staff pages stay closed until they add one and sign in with it.")
		}
		return nil
	case len(args) == 2 && args[0] == "remove":
		u, err := q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(args[1])))
		if err != nil {
			return fmt.Errorf("no account uses %s", args[1])
		}
		n, err := q.RemoveStaff(ctx, u.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%s isn't staff", u.Email)
		}
		if err := q.AddStaffAudit(ctx, store.AddStaffAuditParams{
			StaffID: u.ID, StaffEmail: by, Action: "staff.remove", TargetType: "user", TargetID: id(u.ID),
			Meta: []byte(fmt.Sprintf(`{"email":%q}`, u.Email)),
		}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%s isn't staff any more.\n", u.Email)
		return nil
	case len(args) == 1 && args[0] == "list":
		rows, err := q.ListStaff(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			_, _ = fmt.Fprintf(out, "%-8s %s (added %s by %s)\n", r.Role, r.Email, r.AddedAt.Time.Format("2006-01-02"), r.AddedBy)
		}
		if len(rows) == 0 {
			_, _ = fmt.Fprintln(out, "No staff yet: panel staff add <email> admin")
		}
		return nil
	}
	return errors.New("usage: panel staff add <email> admin|support | remove <email> | list")
}
