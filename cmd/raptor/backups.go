package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

const backupUsage = `usage: raptor backup <command>

  list [<server>] [-json]           backups of a server, or of every server
  create <server> [-lock] [-no-wait]
                                    back up now (root); waits until it's done
  restore <server> <backup> [-yes] [-no-wait]
                                    replace the server's files with a backup
                                    (root); a safety backup is taken first

  key                               show the backup key (root): every backup
                                    is encrypted with it; keep it somewhere
                                    safe, away from this machine

<backup> is a backup's ID or its last 8 characters, from list.`

func backupCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{""}
	}
	switch args[0] {
	case "list", "ls":
		return backupList(ctx, args[1:])
	case "create":
		return backupCreate(ctx, args[1:])
	case "restore":
		return backupRestore(ctx, args[1:], os.Stdin, isTerminal(os.Stdin))
	case "key":
		return backupKey(ctx, args[1:])
	}
	fmt.Fprintln(os.Stderr, backupUsage)
	os.Exit(2)
	return nil
}

func backupList(ctx context.Context, args []string) error {
	var asJSON *bool
	c, pos, err := dial("backup list", args, func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") })
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return errors.New("usage: raptor backup list [<server>]")
	}
	req := &localv1.ListBackupsRequest{}
	if len(pos) == 1 {
		req.Server = pos[0]
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := c.ListBackups(ctx, req)
	if err != nil {
		return rpcErr(err)
	}
	if *asJSON {
		b, err := protojson.Marshal(res)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	if len(res.GetBackups()) == 0 {
		fmt.Println("no backups yet")
		return nil
	}
	return printBackups(os.Stdout, res.GetBackups(), req.Server == "")
}

// printBackups prints backups as a table; withServer adds the server
// column (for the node-wide list).
func printBackups(out io.Writer, list []*localv1.BackupInfo, withServer bool) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	head := "ID\tCREATED\tKIND\tSTATUS\tSIZE\tNEW\tDESTINATION\tNOTES"
	if withServer {
		head = "ID\tSERVER\tCREATED\tKIND\tSTATUS\tSIZE\tNEW\tDESTINATION\tNOTES"
	}
	_, _ = fmt.Fprintln(w, head)
	for _, b := range list {
		size, uploaded := "-", "-"
		if b.GetStatus() == "ok" {
			size, uploaded = human(b.GetSizeBytes()), human(b.GetUploadedBytes())
		}
		var notes []string
		if b.GetLocked() {
			notes = append(notes, "locked")
		}
		if b.GetExpiresAt() != nil {
			notes = append(notes, "expires "+b.GetExpiresAt().AsTime().Local().Format(time.DateOnly))
		}
		if b.GetError() != "" {
			notes = append(notes, truncate(b.GetError(), 50))
		}
		if b.GetWarning() != "" {
			notes = append(notes, "warning: "+truncate(b.GetWarning(), 50))
		}
		row := []string{short(b.GetId())}
		if withServer {
			name := b.GetServerName()
			if name == "" {
				name = short(b.GetServerId()) + " (deleted)"
			}
			row = append(row, name)
		}
		row = append(row, b.GetCreatedAt().AsTime().Local().Format("2006-01-02 15:04"), b.GetKind(), b.GetStatus(),
			size, uploaded, b.GetDestinationId(), strings.Join(notes, "; "))
		_, _ = fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	return w.Flush()
}

func short(id string) string {
	if len(id) > shortID {
		return id[len(id)-shortID:]
	}
	return id
}

func backupCreate(ctx context.Context, args []string) error {
	var lock, noWait *bool
	c, pos, err := dial("backup create", args, func(fs *flag.FlagSet) {
		lock = fs.Bool("lock", false, "never delete it by retention")
		noWait = fs.Bool("no-wait", false, "return once the backup is queued")
	})
	if err != nil {
		return err
	}
	ref, err := oneServer("backup create", pos)
	if err != nil {
		return err
	}
	if !*noWait {
		fmt.Fprintf(os.Stderr, "backing up %s…\n", ref)
	}
	res, err := c.CreateBackup(ctx, &localv1.CreateBackupRequest{Server: ref, Locked: *lock, Wait: !*noWait})
	if err != nil {
		return rpcErr(err)
	}
	b := res.GetBackup()
	if *noWait {
		fmt.Printf("backup %s queued (job %s)\n", short(b.GetId()), b.GetJobId())
		return nil
	}
	fmt.Printf("backup %s: %d files, %s (%s new) to %s\n", short(b.GetId()), b.GetFiles(), human(b.GetSizeBytes()), human(b.GetUploadedBytes()), b.GetDestinationId())
	if b.GetWarning() != "" {
		fmt.Println("warning:", b.GetWarning())
	}
	return nil
}

func backupRestore(ctx context.Context, args []string, in io.Reader, tty bool) error {
	var yes, noWait *bool
	c, pos, err := dial("backup restore", args, func(fs *flag.FlagSet) {
		yes = fs.Bool("yes", false, "don't ask for confirmation")
		noWait = fs.Bool("no-wait", false, "return once the restore is queued")
	})
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("usage: raptor backup restore <server> <backup> (see raptor backup list <server>)")
	}
	ref, backupRef := pos[0], pos[1]
	if !*yes {
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		res, err := c.ListBackups(lctx, &localv1.ListBackupsRequest{Server: ref})
		if err != nil {
			return rpcErr(err)
		}
		if err := confirmRestore(res.GetBackups(), backupRef, in, os.Stderr, tty); err != nil {
			return err
		}
	}
	if !*noWait {
		fmt.Fprintf(os.Stderr, "restoring %s…\n", ref)
	}
	res, err := c.RestoreBackup(ctx, &localv1.RestoreBackupRequest{Server: ref, Backup: backupRef, Wait: !*noWait})
	if err != nil {
		return rpcErr(err)
	}
	switch {
	case *noWait:
		fmt.Printf("restore queued (job %s)\n", res.GetJobId())
	case res.GetSafetyBackupId() != "":
		fmt.Printf("restored; the files it replaced are in safety backup %s (kept 7 days)\n", short(res.GetSafetyBackupId()))
	default:
		fmt.Println("restored")
	}
	return nil
}

// confirmRestore shows what a restore will do and asks for the server's name.
// Without a terminal it refuses: -yes must be explicit.
func confirmRestore(list []*localv1.BackupInfo, ref string, in io.Reader, out io.Writer, tty bool) error {
	var b *localv1.BackupInfo
	for _, x := range list {
		if x.GetId() == ref || (len(ref) >= shortID && strings.HasSuffix(x.GetId(), ref)) {
			b = x
			break
		}
	}
	if b == nil {
		return fmt.Errorf("the server has no backup %q (see raptor backup list)", ref)
	}
	if !tty {
		return errors.New("restoring replaces the server's files; run it in a terminal to confirm, or pass -yes")
	}
	name := b.GetServerName()
	_, _ = fmt.Fprintf(out, "This replaces all files of %s with backup %s from %s (%s).\n",
		name, short(b.GetId()), b.GetCreatedAt().AsTime().Local().Format("2006-01-02 15:04"), human(b.GetSizeBytes()))
	_, _ = fmt.Fprintln(out, "The server is stopped meanwhile. A safety backup of the current files is taken first.")
	_, _ = fmt.Fprintf(out, "Type the server's name (%s) to continue: ", name)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(line) != name {
		return errors.New("not restored")
	}
	return nil
}

func backupKey(ctx context.Context, args []string) error {
	c, pos, err := dial("backup key", args, nil)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return errors.New("usage: raptor backup key")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := c.GetBackupKey(ctx, &localv1.GetBackupKeyRequest{})
	if err != nil {
		return rpcErr(err)
	}
	fmt.Println(res.GetKey())
	who := "Raptor keeps an encrypted copy, so backups can be recovered on another machine."
	if res.GetMode() == "owner" {
		who = "Only you keep it: if it's lost, so are this node's backups."
	}
	fmt.Fprintf(os.Stderr, "\nFingerprint %s. %s\nKeep it somewhere safe, away from this machine.\n", res.GetFingerprint(), who)
	return nil
}
