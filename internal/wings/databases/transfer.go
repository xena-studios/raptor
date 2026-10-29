package databases

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Imports and exports (docs/DATABASES.md#the-browser) go through the
// server's files, which already move big files both ways: an import runs a
// .sql or .sql.gz file the server has; an export writes one (or a CSV) to
// its database-exports folder, for the file manager to download. Dumps
// and imports use the engine's own tools, run in the instance's container
// as the database's own user, their input and output streamed through
// Docker: nothing is written in the container, and passwords go in the
// environment, never on a command line.

// Jobs.
const (
	JobImport = "db.import"
	JobExport = "db.export"
)

// ExportDir is where exports go, in the server's files.
const ExportDir = "database-exports"

// Files opens a server's files, checking they may be used now.
type Files interface {
	Open(ctx context.Context, id string, write bool) (*files.FS, error)
}

// SetFiles gives the service the server's files, once there are any (the
// files service is made after the server manager).
func (s *Service) SetFiles(f Files) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = f
}

func (s *Service) fileService() (Files, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		return nil, errors.New("the server's files aren't ready yet")
	}
	return s.files, nil
}

// ImportJob is an import's payload.
type ImportJob struct {
	Database string `json:"database"`
	Path     string `json:"path"` // in the server's files
}

// ExportJob is an export's payload.
type ExportJob struct {
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
	Table    string `json:"table,omitempty"` // "" is the whole database
	// Query: a CSV of this SQL's whole result instead, run like a reader's
	// query (one statement, read-only, as the database's reader).
	Query  string `json:"query,omitempty"`
	Format string `json:"format"` // sql or csv
	Path   string `json:"path"`   // the file it makes
}

// Transfer is how an import or export is going.
type Transfer struct {
	JobID    string `json:"job_id"`
	Kind     string `json:"kind"` // import, export, or restore (Path is the export of what it had)
	Database string `json:"database"`
	Path     string `json:"path"`
	Status   string `json:"status"` // the job's: queued, running, succeeded, failed, cancelled
	Bytes    int64  `json:"bytes"`  // read so far (import) or written (export)
	Total    int64  `json:"total,omitempty"`
	Error    string `json:"error,omitempty"`
}

type progress struct{ bytes, total atomic.Int64 }

type transfers struct {
	mu sync.Mutex
	m  map[string]*progress
}

func (t *transfers) start(id string) *progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]*progress{}
	}
	p := &progress{}
	t.m[id] = p
	return p
}

func (t *transfers) get(id string) *progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.m[id]
}

func (t *transfers) done(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, id)
}

// counter counts bytes through a reader or writer.
type counter struct {
	r io.Reader
	w io.Writer
	n *atomic.Int64
}

func (c counter) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n.Add(int64(n))
	return n, err
}

func (c counter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n.Add(int64(n))
	return n, err
}

// tailBuffer keeps the end of what's written to it: a tool's errors.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 16<<10 {
		t.buf = t.buf[len(t.buf)-16<<10:]
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

func (s *Service) registerJobs() {
	if s.o.Jobs == nil {
		return
	}
	s.o.Jobs.Register(JobImport, jobs.Handler{Class: "files", Run: s.importJob})
	s.o.Jobs.Register(JobExport, jobs.Handler{Class: "files", Run: s.exportJob})
	s.o.Jobs.Register(JobRestore, jobs.Handler{Class: "files", Run: s.restoreJob})
	s.o.Jobs.Register(JobVersion, jobs.Handler{Class: "database", Run: s.versionJob})
	s.o.Jobs.Register(JobGoBack, jobs.Handler{Class: "database", Run: s.goBackJob})
	s.o.Jobs.Register(JobInstanceRestore, jobs.Handler{Class: "database", Run: s.instanceRestoreJob})
	s.o.Jobs.Register(JobInstanceDelete, jobs.Handler{Class: "database", Run: s.instanceDeleteJob})
}

// tools are an engine's client, its dump, and how both reach the socket.
func tools(engine string) (client, dump string, conn []string, pwEnv string) {
	switch engine {
	case MariaDB:
		return "mariadb", "mariadb-dump", []string{"--socket=/run/mysqld/mysqld.sock"}, "MYSQL_PWD"
	case MySQL:
		return "mysql", "mysqldump", []string{"--socket=/var/run/mysqld/mysqld.sock"}, "MYSQL_PWD"
	}
	return "psql", "pg_dump", []string{"--host=/var/run/postgresql"}, "PGPASSWORD"
}

// Import queues running a file of the server's against one of its
// databases.
func (s *Service) Import(ctx context.Context, serverID, id, file string) (string, error) {
	if s.o.Jobs == nil {
		return "", errors.New("imports aren't available")
	}
	if _, err := s.database(ctx, serverID, id); err != nil {
		return "", err
	}
	f, err := s.fileService()
	if err != nil {
		return "", err
	}
	fsys, err := f.Open(ctx, serverID, false)
	if err != nil {
		return "", err
	}
	defer func() { _ = fsys.Close() }()
	file = files.Rel(file)
	fh, err := fsys.Open(file, os.O_RDONLY)
	if err != nil {
		return "", err
	}
	_ = fh.Close()
	engine := s.engineOf(ctx, serverID, id)
	switch {
	case isRedis(engine):
		return "", invalid{engineName(engine) + " has no imports: restore its data from a backup instead"}
	case engine == MongoDB:
		if !strings.HasSuffix(file, ".archive") && !strings.HasSuffix(file, ".gz") {
			return "", invalid{"pick a mongodump archive: a .archive or .archive.gz file"}
		}
	case !strings.HasSuffix(file, ".sql") && !strings.HasSuffix(file, ".sql.gz") && !strings.HasSuffix(file, ".gz"):
		return "", invalid{"pick a .sql or .sql.gz file"}
	}
	return s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobImport, ServerID: serverID, Payload: ImportJob{Database: id, Path: file}})
}

func (s *Service) importJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p ImportJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	pr := s.transfers.start(j.ID)
	defer s.transfers.done(j.ID)
	r, err := s.database(ctx, j.ServerID, p.Database)
	if err != nil {
		return nil, err
	}
	in, _, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return nil, err
	}
	if s.state(r.InstanceID) != StateRunning {
		return nil, notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	f, err := s.fileService()
	if err != nil {
		return nil, err
	}
	fsys, err := f.Open(ctx, j.ServerID, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fsys.Close() }()
	fh, err := fsys.Open(p.Path, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	if st, err := fh.Stat(); err == nil {
		pr.total.Store(st.Size())
	}
	var src io.Reader = bufio.NewReaderSize(counter{r: fh, n: &pr.bytes}, 1<<20)
	// gzip by its first bytes, whatever the name says.
	if head, _ := src.(*bufio.Reader).Peek(2); bytes.Equal(head, []byte{0x1f, 0x8b}) {
		gz, err := gzip.NewReader(src)
		if err != nil {
			return nil, fmt.Errorf("the file isn't valid gzip: %w", err)
		}
		defer func() { _ = gz.Close() }()
		src = gz
	}

	_, _ = fmt.Fprintf(log, "importing /%s into %s\n", p.Path, r.DbName)
	if err := s.importFrom(ctx, in, r, src, log); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(log, "done: %d bytes read\n", pr.bytes.Load())
	return map[string]any{"path": p.Path, "bytes": pr.bytes.Load()}, nil
}

// importFrom runs SQL into a database as its own user, stopping at the
// first error.
func (s *Service) importFrom(ctx context.Context, in Instance, r store.Database, src io.Reader, log io.Writer) error {
	pass, err := s.sealer.open(r.Password)
	if err != nil {
		return err
	}
	if in.Engine == MongoDB {
		// Whatever database the archive is of goes into this one; the
		// server-wide ones (admin, config, local) are left out.
		return s.mongoTool(ctx, r.InstanceID, r.Username, pass, r.DbName, []string{
			"mongorestore", "--archive", "--drop", "--stopOnError",
			"--nsExclude=admin.*", "--nsExclude=config.*", "--nsExclude=local.*",
			"--nsFrom=$db$.$coll$", "--nsTo=" + r.DbName + ".$coll$",
		}, src, nil, log)
	}
	if isRedis(in.Engine) {
		return invalid{"there's no importing into " + engineName(in.Engine) + ": restore its data from a backup"}
	}
	client, _, conn, pwEnv := tools(in.Engine)
	cmd := append([]string{client}, conn...)
	if in.Engine == Postgres {
		cmd = append(cmd, "--username="+r.Username, "--dbname="+r.DbName, "--no-psqlrc", "--quiet", "--set=ON_ERROR_STOP=1")
	} else {
		cmd = append(cmd, "--user="+r.Username, "--database="+r.DbName, "--binary-mode")
	}
	var stderr tailBuffer
	code, err := s.o.Runtime.Exec(ctx, ContainerName(r.InstanceID), containers.ExecSpec{
		Cmd: cmd, Env: []string{pwEnv + "=" + pass}, User: "999:999",
		Stdin: src, Stdout: io.Discard, Stderr: io.MultiWriter(&stderr, log),
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("the import stopped at an error: %s", lastLine(stderr.String()))
	}
	return nil
}

// lastLine is a tool's error, from its output: the last line that names
// one (psql follows it with the statement and a ^ under the spot), or
// else the last line.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "ERROR") {
			return strings.TrimSpace(lines[i])
		}
	}
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return "no reason given"
}

// Export queues a dump of one of a server's databases, or one of its
// tables, into the server's database-exports folder, and returns the job's
// ID and the file it makes.
func (s *Service) Export(ctx context.Context, serverID, id, schema, table, format string) (string, string, error) {
	if s.o.Jobs == nil {
		return "", "", errors.New("exports aren't available")
	}
	switch engine := s.engineOf(ctx, serverID, id); {
	case isRedis(engine):
		return "", "", invalid{engineName(engine) + " has no exports: its data is in the server's backups"}
	case engine == MongoDB:
		format, schema = "archive", ""
	case format != "sql" && format != "csv":
		return "", "", invalid{"export as sql or csv"}
	}
	if format == "csv" && table == "" {
		return "", "", invalid{"a CSV is of one table: pick it"}
	}
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return "", "", err
	}
	if table != "" && format != "archive" {
		// Checked now, so a typo fails here rather than in the job.
		se, err := s.open(ctx, serverID, id, false)
		if err != nil {
			return "", "", err
		}
		_, err = se.table(ctx, schema, table)
		se.Close()
		if err != nil {
			return "", "", err
		}
	}
	name := r.Label
	if table != "" {
		name += "-" + safeFile(table)
	}
	name += "-" + time.Now().UTC().Format("20060102-150405")
	switch format {
	case "csv":
		name += ".csv"
	case "archive":
		name += ".archive.gz"
	default:
		name += ".sql.gz"
	}
	target := path.Join(ExportDir, name)
	jobID, err := s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobExport, ServerID: serverID, Payload: ExportJob{
		Database: id, Schema: schema, Table: table, Format: format, Path: target,
	}})
	return jobID, target, err
}

// ExportQuery queues a CSV of a query's whole result into the server's
// database-exports folder, and returns the job's ID and the file it
// makes. The query runs as a reader's does: one statement, in a read-only
// transaction, as the database's reader, so an export changes nothing.
func (s *Service) ExportQuery(ctx context.Context, serverID, id string, in QueryInput) (string, string, error) {
	if s.o.Jobs == nil {
		return "", "", errors.New("exports aren't available")
	}
	text, _, err := in.check(MaxStreamTimeout)
	if err != nil {
		return "", "", err
	}
	r, _, _, err := s.browsable(ctx, serverID, id)
	if err != nil {
		return "", "", err
	}
	target := path.Join(ExportDir, r.Label+"-query-"+time.Now().UTC().Format("20060102-150405")+".csv")
	jobID, err := s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobExport, ServerID: serverID, Payload: ExportJob{
		Database: id, Query: text, Format: "csv", Path: target,
	}})
	return jobID, target, err
}

// exportQueryCSV writes a query's whole result as CSV, with its column
// names first.
func (s *Service) exportQueryCSV(ctx context.Context, serverID string, p ExportJob, w io.Writer) error {
	se, err := s.openViewer(ctx, serverID, p.Database)
	if err != nil {
		return err
	}
	defer se.Close()
	c := &csvSink{w: csv.NewWriter(w)}
	// The job has no time limit of its own; PostgreSQL's statement_timeout
	// is off with 0.
	if _, err := se.run(ctx, p.Query, true, 0, c); err != nil {
		return err
	}
	if c.err != nil {
		return c.err
	}
	c.w.Flush()
	return c.w.Error()
}

// csvSink writes a query's result as CSV.
type csvSink struct {
	w   *csv.Writer
	n   int
	rec []string
	err error
}

func (c *csvSink) columns(cols []ResultColumn) {
	c.n = len(cols)
	header := make([]string, len(cols))
	for i, col := range cols {
		header[i] = col.Name
	}
	if c.err == nil {
		c.err = c.w.Write(header)
	}
	c.rec = make([]string, c.n)
}

func (c *csvSink) row(row []*string) error {
	if c.err != nil {
		return c.err
	}
	for i := range c.rec {
		c.rec[i] = ""
		if i < len(row) && row[i] != nil {
			c.rec[i] = *row[i]
		}
	}
	c.err = c.w.Write(c.rec)
	return c.err
}

// safeFile is a table's name made safe as part of a file name.
func safeFile(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

func (s *Service) exportJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p ExportJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	pr := s.transfers.start(j.ID)
	defer s.transfers.done(j.ID)
	_, _ = fmt.Fprintf(log, "exporting to /%s\n", p.Path)
	if err := s.exportTo(ctx, j.ServerID, p, &pr.bytes, log); err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(log, "done: %d bytes\n", pr.bytes.Load())
	return map[string]any{"path": p.Path, "bytes": pr.bytes.Load()}, nil
}

// exportTo writes an export into the server's files, through a .part file
// renamed when it's done, counting the bytes written in n.
func (s *Service) exportTo(ctx context.Context, serverID string, p ExportJob, n *atomic.Int64, log io.Writer) error {
	f, err := s.fileService()
	if err != nil {
		return err
	}
	fsys, err := f.Open(ctx, serverID, true)
	if err != nil {
		return err
	}
	defer func() { _ = fsys.Close() }()
	if err := fsys.MkdirAll(ExportDir); err != nil {
		return err
	}
	partial := p.Path + ".part"
	out, err := fsys.Open(partial, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = fsys.Remove(partial)
		}
	}()
	w := counter{w: out, n: n}
	switch {
	case p.Query != "":
		err = s.exportQueryCSV(ctx, serverID, p, w)
	case p.Format == "csv":
		err = s.exportCSV(ctx, serverID, p, w)
	default:
		gz := gzip.NewWriter(w)
		err = s.dump(ctx, serverID, p, gz, log)
		if cerr := gz.Close(); err == nil {
			err = cerr
		}
	}
	if err == nil {
		err = out.Sync()
	}
	if err != nil {
		return err
	}
	if err := fsys.Rename(partial, p.Path, false); err != nil {
		return err
	}
	ok = true
	return nil
}

// dump writes the engine's dump of a database or a table.
func (s *Service) dump(ctx context.Context, serverID string, p ExportJob, w io.Writer, log io.Writer) error {
	r, err := s.database(ctx, serverID, p.Database)
	if err != nil {
		return err
	}
	return s.dumpTo(ctx, r, p.Schema, p.Table, w, log)
}

// dumpTo writes a dump of a database, or one of its tables, as its own
// user: plain SQL, without owners or privileges, so it can be run into any
// database by its user.
func (s *Service) dumpTo(ctx context.Context, r store.Database, schema, table string, w io.Writer, log io.Writer) error {
	in, _, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return err
	}
	if s.state(r.InstanceID) != StateRunning {
		return notReady{fmt.Sprintf("%s isn't running", in.Name)}
	}
	pass, err := s.sealer.open(r.Password)
	if err != nil {
		return err
	}
	switch in.Engine {
	case MongoDB:
		args := []string{"mongodump", "--archive", "--db=" + r.DbName}
		if table != "" {
			args = append(args, "--collection="+table)
		}
		return s.mongoTool(ctx, r.InstanceID, r.Username, pass, r.DbName, args, nil, w, log)
	case Redis, Valkey:
		_, ir, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
		if err != nil {
			return err
		}
		root, err := s.sealer.open(ir.RootPassword)
		if err != nil {
			return err
		}
		return redisDump(ctx, s.dir(r.InstanceID), root, w)
	}
	_, dump, conn, pwEnv := tools(in.Engine)
	cmd := append([]string{dump}, conn...)
	switch in.Engine {
	case Postgres:
		cmd = append(cmd, "--username="+r.Username, "--dbname="+r.DbName, "--no-owner", "--no-privileges")
		if table != "" {
			if schema == "" {
				schema = "public"
			}
			cmd = append(cmd, "--table="+quotePG(schema)+"."+quotePG(table))
		}
	default:
		// A consistent snapshot without locking, and nothing that needs
		// rights the database's user doesn't have.
		cmd = append(cmd, "--user="+r.Username, "--single-transaction", "--no-tablespaces", "--skip-lock-tables", "--triggers")
		if in.Engine == MySQL {
			cmd = append(cmd, "--set-gtid-purged=OFF")
		}
		cmd = append(cmd, r.DbName)
		if table != "" {
			cmd = append(cmd, table)
		}
	}
	var stderr tailBuffer
	code, err := s.o.Runtime.Exec(ctx, ContainerName(r.InstanceID), containers.ExecSpec{
		Cmd: cmd, Env: []string{pwEnv + "=" + pass}, User: "999:999",
		Stdout: w, Stderr: io.MultiWriter(&stderr, log),
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("the dump failed: %s", lastLine(stderr.String()))
	}
	return nil
}

// quotePG quotes a name for pg_dump's --table, which reads it like SQL.
func quotePG(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// exportCSV writes a table's rows as CSV, with its column names first.
func (s *Service) exportCSV(ctx context.Context, serverID string, p ExportJob, w io.Writer) error {
	se, err := s.open(ctx, serverID, p.Database, false)
	if err != nil {
		return err
	}
	defer se.Close()
	t, err := se.table(ctx, p.Schema, p.Table)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = c.Name
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	rec := make([]string, len(t.Columns))
	err = se.eachRow(ctx, "SELECT * FROM "+se.tableRef(t), func(row []*string) error {
		for i := range rec {
			rec[i] = ""
			if i < len(row) && row[i] != nil {
				rec[i] = *row[i]
			}
		}
		return cw.Write(rec)
	})
	if err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// eachRow streams a query's rows as text, one at a time.
func (se *session) eachRow(ctx context.Context, query string, fn func([]*string) error) error {
	if se.pg != nil {
		rows, err := se.pg.Query(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			raw := rows.RawValues()
			row := make([]*string, len(raw))
			for i, v := range raw {
				if v != nil {
					s := string(v)
					row[i] = &s
				}
			}
			if err := fn(row); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	rows, err := se.my.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	raw := make([]sql.RawBytes, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]*string, len(cols))
		for i, v := range raw {
			if v != nil {
				s := string(v)
				row[i] = &s
			}
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

// TransferStatus says how an import or export is going.
func (s *Service) TransferStatus(ctx context.Context, serverID, jobID string) (Transfer, error) {
	j, err := s.o.Jobs.Get(ctx, jobID)
	if err != nil || j.ServerID != serverID || (j.Type != JobImport && j.Type != JobExport && j.Type != JobRestore) {
		return Transfer{}, fmt.Errorf("%w: job %s", ErrNotFound, jobID)
	}
	t := Transfer{JobID: j.ID, Status: j.Status, Error: j.Error}
	switch j.Type {
	case JobImport:
		var p ImportJob
		_ = j.Decode(&p)
		t.Kind, t.Database, t.Path = "import", p.Database, p.Path
	case JobRestore:
		var p RestoreJob
		_ = j.Decode(&p)
		t.Kind, t.Database, t.Path = "restore", p.Database, p.Before
	default:
		var p ExportJob
		_ = j.Decode(&p)
		t.Kind, t.Database, t.Path = "export", p.Database, p.Path
	}
	if pr := s.transfers.get(j.ID); pr != nil {
		t.Bytes, t.Total = pr.bytes.Load(), pr.total.Load()
	}
	return t, nil
}

// CancelTransfer stops an import or export. An import stops after the
// statement it's on; what ran before stays.
func (s *Service) CancelTransfer(ctx context.Context, serverID, jobID string) error {
	if _, err := s.TransferStatus(ctx, serverID, jobID); err != nil {
		return err
	}
	return s.o.Jobs.Cancel(ctx, jobID)
}

// engineOf is the engine of one of a server's databases, "" if it can't
// be read (the caller's own lookups say why).
func (s *Service) engineOf(ctx context.Context, serverID, id string) string {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return ""
	}
	in, _, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return ""
	}
	return in.Engine
}
