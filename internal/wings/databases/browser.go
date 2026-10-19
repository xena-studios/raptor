package databases

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The browser (docs/DATABASES.md#the-browser): a server's database seen
// and changed through typed commands. Wings connects as the database's own
// user, through the instance's socket, so the browser never reaches
// further than that user's rights. Table and column names are checked
// against the schema before they go into SQL, and values are always
// parameters.

// Bounds on what one command returns.
const (
	MaxRows        = 1000    // rows in one result
	maxCell        = 8 << 10 // bytes of one value, then it's cut
	maxResultBytes = 2 << 20 // bytes of values in one result
	DefaultTimeout = 30      // seconds a query may run by default
	MaxTimeout     = 50      // and at most: a command has a minute
	maxSQL         = 1 << 20 // bytes of SQL in one query
	maxEditValue   = 1 << 20 // bytes of one value written
	maxFilters     = 10
)

// Table is a table or view, with its columns and indexes.
type Table struct {
	Schema     string   `json:"schema,omitempty"` // PostgreSQL's; "" on MariaDB and MySQL
	Name       string   `json:"name"`
	Kind       string   `json:"kind"` // table or view
	Rows       int64    `json:"rows"` // the engine's estimate
	Bytes      int64    `json:"bytes"`
	Columns    []Column `json:"columns"`
	PrimaryKey []string `json:"primary_key"`
	Indexes    []Index  `json:"indexes"`
}

// Column is one of a table's columns.
type Column struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Nullable bool    `json:"nullable"`
	Default  *string `json:"default,omitempty"`
	Auto     bool    `json:"auto,omitempty"` // filled in by the engine (auto increment, identity)
}

// Index is one of a table's indexes.
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary,omitempty"`
}

// Result is what a query returned.
type Result struct {
	Columns []ResultColumn `json:"columns"`
	// Rows are values as text; nil is NULL. Binary values are 0x and hex.
	Rows [][]*string `json:"rows"`
	// More is set when there were more rows than returned.
	More bool `json:"more,omitempty"`
	// Cut is set when a long value was shortened.
	Cut bool `json:"cut,omitempty"`
	// Affected is how many rows a statement changed; -1 when it returned
	// rows instead.
	Affected   int64 `json:"affected"`
	Statements int   `json:"statements,omitempty"`
	Millis     int64 `json:"millis"`
}

// ResultColumn is one column of a result.
type ResultColumn struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// session is a connection to one database as its own user.
type session struct {
	engine string
	in     Instance
	dir    string
	root   string // for killing a query that ran out of time
	my     *sql.DB
	pg     *pgx.Conn
	name   string
}

func (se *session) Close() {
	if se.my != nil {
		_ = se.my.Close()
	}
	if se.pg != nil {
		_ = se.pg.Close(context.Background())
	}
}

// open connects to one of a server's databases as its user.
func (s *Service) open(ctx context.Context, serverID, id string, multi bool) (*session, error) {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return nil, err
	}
	in, ir, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return nil, err
	}
	if s.state(r.InstanceID) != StateRunning {
		return nil, notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	pass, err := s.sealer.open(r.Password)
	if err != nil {
		return nil, err
	}
	root, err := s.sealer.open(ir.RootPassword)
	if err != nil {
		return nil, err
	}
	dir := s.dir(r.InstanceID)
	se := &session{engine: in.Engine, in: in, dir: dir, root: root, name: r.DbName}
	switch in.Engine {
	case MariaDB, MySQL:
		cfg := mysql.NewConfig()
		cfg.User, cfg.Passwd, cfg.DBName = r.Username, pass, r.DbName
		cfg.Net, cfg.Addr = "unix", filepath.Join(runDir(dir), "mysqld.sock")
		cfg.Timeout = 5 * time.Second
		cfg.MultiStatements = multi
		c, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, err
		}
		se.my = sql.OpenDB(c)
		se.my.SetMaxOpenConns(1)
	case Postgres:
		cfg, err := pgx.ParseConfig("")
		if err != nil {
			return nil, err
		}
		cfg.Host, cfg.Port = runDir(dir), 5432
		cfg.User, cfg.Password, cfg.Database = r.Username, pass, r.DbName
		cfg.ConnectTimeout = 5 * time.Second
		// Values are sent as literals the engine casts to the column's
		// type, so a string can go into any column.
		cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
		if se.pg, err = pgx.ConnectConfig(ctx, cfg); err != nil {
			return nil, fmt.Errorf("connecting to the database: %w", err)
		}
	}
	return se, nil
}

// quote is a name as an identifier.
func (se *session) quote(name string) string {
	if se.engine == Postgres {
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// tableRef is a table's quoted, schema-qualified name.
func (se *session) tableRef(t Table) string {
	if se.engine == Postgres {
		return se.quote(t.Schema) + "." + se.quote(t.Name)
	}
	return se.quote(t.Name)
}

// textRows runs a query returning text values.
func (se *session) textRows(ctx context.Context, query string, args ...any) ([][]*string, error) {
	var out [][]*string
	if se.pg != nil {
		rows, err := se.pg.Query(ctx, query, args...)
		if err != nil {
			return nil, err
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
			out = append(out, row)
		}
		return out, rows.Err()
	}
	rows, err := se.my.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		raw := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]*string, len(cols))
		for i, v := range raw {
			if v != nil {
				s := string(v)
				row[i] = &s
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func num(p *string) int64 {
	n, _ := strconv.ParseInt(strings.SplitN(str(p), ".", 2)[0], 10, 64)
	return n
}

// Schema returns a database's tables and views.
func (s *Service) Schema(ctx context.Context, serverID, id string) ([]Table, error) {
	se, err := s.open(ctx, serverID, id, false)
	if err != nil {
		return nil, err
	}
	defer se.Close()
	return se.schema(ctx)
}

func (se *session) schema(ctx context.Context) ([]Table, error) {
	var tables []Table
	byName := map[string]int{}
	key := func(schema, name string) string { return schema + "\x00" + name }
	add := func(schema, name, kind string, rows, size int64) {
		byName[key(schema, name)] = len(tables)
		tables = append(tables, Table{Schema: schema, Name: name, Kind: kind, Rows: rows, Bytes: size, Columns: []Column{}, PrimaryKey: []string{}, Indexes: []Index{}})
	}
	if se.engine == Postgres {
		rows, err := se.textRows(ctx, `SELECT n.nspname, c.relname, c.relkind, GREATEST(c.reltuples, 0)::bigint, pg_total_relation_size(c.oid)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'v', 'm', 'p', 'f') AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'
ORDER BY 1, 2`)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			kind := "table"
			if k := str(r[2]); k == "v" || k == "m" {
				kind = "view"
			}
			add(str(r[0]), str(r[1]), kind, num(r[3]), num(r[4]))
		}
		cols, err := se.textRows(ctx, `SELECT n.nspname, c.relname, a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull,
  pg_get_expr(d.adbin, d.adrelid), a.attidentity <> '' OR COALESCE(pg_get_expr(d.adbin, d.adrelid), '') LIKE 'nextval(%'
FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attnum > 0 AND NOT a.attisdropped AND c.relkind IN ('r', 'v', 'm', 'p', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY 1, 2, a.attnum`)
		if err != nil {
			return nil, err
		}
		for _, r := range cols {
			if i, ok := byName[key(str(r[0]), str(r[1]))]; ok {
				tables[i].Columns = append(tables[i].Columns, Column{
					Name: str(r[2]), Type: str(r[3]), Nullable: str(r[4]) == "t", Default: r[5], Auto: str(r[6]) == "t",
				})
			}
		}
		idx, err := se.textRows(ctx, `SELECT n.nspname, t.relname, i.relname, ix.indisunique, ix.indisprimary,
  array_to_json(ARRAY(SELECT a.attname FROM unnest(ix.indkey) WITH ORDINALITY k(attnum, ord)
    JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum ORDER BY k.ord))::text
FROM pg_index ix JOIN pg_class t ON t.oid = ix.indrelid JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%'
ORDER BY 1, 2, 3`)
		if err != nil {
			return nil, err
		}
		for _, r := range idx {
			i, ok := byName[key(str(r[0]), str(r[1]))]
			if !ok {
				continue
			}
			var cols []string
			_ = json.Unmarshal([]byte(str(r[5])), &cols)
			x := Index{Name: str(r[2]), Columns: cols, Unique: str(r[3]) == "t", Primary: str(r[4]) == "t"}
			tables[i].Indexes = append(tables[i].Indexes, x)
			if x.Primary {
				tables[i].PrimaryKey = cols
			}
		}
		return tables, nil
	}

	rows, err := se.textRows(ctx, `SELECT TABLE_NAME, TABLE_TYPE, COALESCE(TABLE_ROWS, 0), COALESCE(DATA_LENGTH, 0) + COALESCE(INDEX_LENGTH, 0)
FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME`)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		kind := "table"
		if strings.Contains(str(r[1]), "VIEW") {
			kind = "view"
		}
		add("", str(r[0]), kind, num(r[2]), num(r[3]))
	}
	cols, err := se.textRows(ctx, `SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA
FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, ORDINAL_POSITION`)
	if err != nil {
		return nil, err
	}
	for _, r := range cols {
		if i, ok := byName[key("", str(r[0]))]; ok {
			tables[i].Columns = append(tables[i].Columns, Column{
				Name: str(r[1]), Type: str(r[2]), Nullable: str(r[3]) == "YES", Default: r[4],
				Auto: strings.Contains(str(r[5]), "auto_increment"),
			})
		}
	}
	idx, err := se.textRows(ctx, `SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME
FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`)
	if err != nil {
		return nil, err
	}
	for _, r := range idx {
		i, ok := byName[key("", str(r[0]))]
		if !ok {
			continue
		}
		t := &tables[i]
		name := str(r[1])
		j := slices.IndexFunc(t.Indexes, func(x Index) bool { return x.Name == name })
		if j < 0 {
			t.Indexes = append(t.Indexes, Index{Name: name, Unique: str(r[2]) == "0", Primary: name == "PRIMARY"})
			j = len(t.Indexes) - 1
		}
		t.Indexes[j].Columns = append(t.Indexes[j].Columns, str(r[3]))
		if name == "PRIMARY" {
			t.PrimaryKey = t.Indexes[j].Columns
		}
	}
	return tables, nil
}

// table finds a table in the schema; names come from users, so they're
// only ever used once found here.
func (se *session) table(ctx context.Context, schema, name string) (Table, error) {
	tables, err := se.schema(ctx)
	if err != nil {
		return Table{}, err
	}
	for _, t := range tables {
		if t.Name == name && (se.engine != Postgres || t.Schema == schema || (schema == "" && t.Schema == "public")) {
			return t, nil
		}
	}
	return Table{}, invalid{fmt.Sprintf("there's no table %s", name)}
}

func (t Table) column(name string) (Column, bool) {
	i := slices.IndexFunc(t.Columns, func(c Column) bool { return c.Name == name })
	if i < 0 {
		return Column{}, false
	}
	return t.Columns[i], true
}

// Filter is one condition on a column.
type Filter struct {
	Column string  `json:"column"`
	Op     string  `json:"op"` // = != < <= > >= contains null notnull
	Value  *string `json:"value,omitempty"`
}

// RowsQuery picks a page of a table's rows.
type RowsQuery struct {
	Schema  string   `json:"schema,omitempty"`
	Table   string   `json:"table"`
	Offset  int      `json:"offset,omitempty"`
	Limit   int      `json:"limit,omitempty"` // 100 by default, at most MaxRows
	Sort    string   `json:"sort,omitempty"`
	Desc    bool     `json:"desc,omitempty"`
	Filters []Filter `json:"filters,omitempty"`
}

// Rows returns a page of a table's rows.
func (s *Service) Rows(ctx context.Context, serverID, id string, q RowsQuery) (Result, error) {
	se, err := s.open(ctx, serverID, id, false)
	if err != nil {
		return Result{}, err
	}
	defer se.Close()
	t, err := se.table(ctx, q.Schema, q.Table)
	if err != nil {
		return Result{}, err
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > MaxRows || q.Offset < 0 {
		return Result{}, invalid{fmt.Sprintf("a page is at most %d rows", MaxRows)}
	}
	if len(q.Filters) > maxFilters {
		return Result{}, invalid{fmt.Sprintf("at most %d filters", maxFilters)}
	}
	var where []string
	var args []any
	param := func(v any) string {
		args = append(args, v)
		if se.engine == Postgres {
			return "$" + strconv.Itoa(len(args))
		}
		return "?"
	}
	for _, f := range q.Filters {
		if _, ok := t.column(f.Column); !ok {
			return Result{}, invalid{fmt.Sprintf("there's no column %s", f.Column)}
		}
		col := se.quote(f.Column)
		switch f.Op {
		case "null":
			where = append(where, col+" IS NULL")
		case "notnull":
			where = append(where, col+" IS NOT NULL")
		case "contains":
			if se.engine == Postgres {
				where = append(where, col+"::text ILIKE '%' || "+param(str(f.Value))+" || '%'")
			} else {
				where = append(where, col+" LIKE CONCAT('%', "+param(str(f.Value))+", '%')")
			}
		case "=", "!=", "<", "<=", ">", ">=":
			op := f.Op
			if op == "!=" {
				op = "<>"
			}
			where = append(where, col+" "+op+" "+param(str(f.Value)))
		default:
			return Result{}, invalid{fmt.Sprintf("%q isn't a filter", f.Op)}
		}
	}
	query := "SELECT * FROM " + se.tableRef(t)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	order := t.PrimaryKey
	if q.Sort != "" {
		if _, ok := t.column(q.Sort); !ok {
			return Result{}, invalid{fmt.Sprintf("there's no column %s", q.Sort)}
		}
		order = []string{q.Sort}
	}
	if len(order) > 0 {
		parts := make([]string, len(order))
		for i, c := range order {
			parts[i] = se.quote(c)
			if q.Desc {
				parts[i] += " DESC"
			}
		}
		query += " ORDER BY " + strings.Join(parts, ", ")
	}
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", q.Limit+1, q.Offset)

	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout*time.Second)
	defer cancel()
	res := Result{Affected: -1}
	for _, c := range t.Columns {
		res.Columns = append(res.Columns, ResultColumn{Name: c.Name, Type: c.Type})
	}
	rows, err := se.textRows(ctx, query, args...)
	if err != nil {
		return Result{}, err
	}
	res.add(rows, q.Limit)
	res.Millis = time.Since(start).Milliseconds()
	return res, nil
}

// add takes rows into a result, up to its bounds.
func (r *Result) add(rows [][]*string, limit int) {
	size := 0
	r.Rows = [][]*string{}
	for i, row := range rows {
		if i >= limit || size > maxResultBytes {
			r.More = true
			break
		}
		for j, v := range row {
			if v == nil {
				continue
			}
			s := *v
			if !utf8.ValidString(s) {
				s = "0x" + hex.EncodeToString([]byte(s))
			}
			if len(s) > maxCell {
				s = strings.ToValidUTF8(s[:maxCell], "")
				r.Cut = true
			}
			size += len(s)
			row[j] = &s
		}
		r.Rows = append(r.Rows, row)
	}
}

// QueryInput is SQL to run.
type QueryInput struct {
	SQL     string `json:"sql"`
	Timeout int    `json:"timeout,omitempty"` // seconds; DefaultTimeout if 0
}

// Query runs SQL on one of a server's databases. readOnly runs one
// statement in a read-only transaction, for those who may only look;
// otherwise any statements run, and the last one's result is returned.
func (s *Service) Query(ctx context.Context, serverID, id string, in QueryInput, readOnly bool) (Result, error) {
	text := strings.TrimSpace(in.SQL)
	if text == "" {
		return Result{}, invalid{"there's no SQL to run"}
	}
	if len(text) > maxSQL {
		return Result{}, invalid{"the SQL is over 1 MB"}
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		return Result{}, invalid{fmt.Sprintf("a query runs for at most %d seconds", MaxTimeout)}
	}
	se, err := s.open(ctx, serverID, id, !readOnly)
	if err != nil {
		return Result{}, err
	}
	defer se.Close()
	qctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	start := time.Now()
	var res Result
	if se.engine == Postgres {
		res, err = se.queryPG(qctx, text, readOnly, timeout)
	} else {
		res, err = se.queryMy(qctx, text, readOnly)
	}
	// PostgreSQL's own statement_timeout may stop it first (57014).
	var pgErr *pgconn.PgError
	if errors.Is(qctx.Err(), context.DeadlineExceeded) || (errors.As(err, &pgErr) && pgErr.Code == "57014") {
		return Result{}, invalid{fmt.Sprintf("it ran for over %d seconds and was stopped", timeout)}
	}
	if err != nil {
		return Result{}, err
	}
	res.Millis = time.Since(start).Milliseconds()
	return res, nil
}

func (se *session) queryPG(ctx context.Context, text string, readOnly bool, timeout int) (Result, error) {
	if _, err := se.pg.Exec(ctx, fmt.Sprintf("SET statement_timeout = %d", timeout*1000)); err != nil {
		return Result{}, err
	}
	if readOnly {
		if _, err := se.pg.Exec(ctx, "BEGIN READ ONLY"); err != nil {
			return Result{}, err
		}
		defer func() { _, _ = se.pg.Exec(context.WithoutCancel(ctx), "ROLLBACK") }()
		// One statement only: the extended protocol takes no more, so
		// nothing can end the read-only transaction and go on.
		rows, err := se.pg.Query(ctx, text, pgx.QueryExecModeDescribeExec, pgx.QueryResultFormats{pgx.TextFormatCode})
		if err != nil {
			return Result{}, err
		}
		defer rows.Close()
		res := Result{Affected: -1, Statements: 1}
		for _, f := range rows.FieldDescriptions() {
			res.Columns = append(res.Columns, ResultColumn{Name: f.Name})
		}
		var out [][]*string
		for rows.Next() && len(out) <= MaxRows {
			raw := rows.RawValues()
			row := make([]*string, len(raw))
			for i, v := range raw {
				if v != nil {
					s := string(v)
					row[i] = &s
				}
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			return Result{}, err
		}
		res.add(out, MaxRows)
		return res, nil
	}
	results, err := se.pg.PgConn().Exec(ctx, text).ReadAll()
	if err != nil {
		return Result{}, err
	}
	res := Result{Affected: -1, Statements: len(results), Rows: [][]*string{}}
	if len(results) == 0 {
		return res, nil
	}
	last := results[len(results)-1]
	if len(last.FieldDescriptions) == 0 {
		res.Affected = last.CommandTag.RowsAffected()
		return res, nil
	}
	for _, f := range last.FieldDescriptions {
		res.Columns = append(res.Columns, ResultColumn{Name: f.Name})
	}
	res.add(pgRows(last), MaxRows)
	return res, nil
}

func pgRows(r *pgconn.Result) [][]*string {
	out := make([][]*string, 0, len(r.Rows))
	for _, raw := range r.Rows {
		row := make([]*string, len(raw))
		for i, v := range raw {
			if v != nil {
				s := string(v)
				row[i] = &s
			}
		}
		out = append(out, row)
	}
	return out
}

// returnsRows guesses whether MariaDB or MySQL SQL returns rows, by its
// first word.
func returnsRows(text string) bool {
	word := strings.ToUpper(strings.TrimLeft(strings.Fields(text + " ")[0], "("))
	switch word {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "WITH", "VALUES", "TABLE", "CHECKSUM", "ANALYZE", "CHECK", "OPTIMIZE":
		return true
	}
	return false
}

func (se *session) queryMy(ctx context.Context, text string, readOnly bool) (Result, error) {
	conn, err := se.my.Conn(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("connecting to the database: %w", err)
	}
	defer func() { _ = conn.Close() }()
	var connID int64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&connID); err != nil {
		return Result{}, err
	}
	// A query that runs out of time is killed on the server too; closing
	// the connection alone leaves it running.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				se.kill(connID)
			}
		case <-done:
		}
	}()
	if readOnly {
		// The session and the transaction are both read-only, and the
		// connection takes one statement at a time.
		for _, stmt := range []string{"SET SESSION TRANSACTION READ ONLY", "START TRANSACTION READ ONLY"} {
			if _, err := conn.ExecContext(ctx, stmt); err != nil {
				return Result{}, err
			}
		}
		defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") }()
	}
	if !returnsRows(text) {
		r, err := conn.ExecContext(ctx, text)
		if err != nil {
			return Result{}, err
		}
		n, _ := r.RowsAffected()
		return Result{Affected: n, Rows: [][]*string{}}, nil
	}
	rows, err := conn.QueryContext(ctx, text)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = rows.Close() }()
	res := Result{Affected: -1}
	cols, err := rows.ColumnTypes()
	if err != nil {
		return Result{}, err
	}
	for _, c := range cols {
		res.Columns = append(res.Columns, ResultColumn{Name: c.Name(), Type: strings.ToLower(c.DatabaseTypeName())})
	}
	var out [][]*string
	for rows.Next() && len(out) <= MaxRows {
		raw := make([]sql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Result{}, err
		}
		row := make([]*string, len(cols))
		for i, v := range raw {
			if v != nil {
				s := string(v)
				row[i] = &s
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return Result{}, err
	}
	res.add(out, MaxRows)
	return res, nil
}

// kill stops a running MariaDB or MySQL query, as root.
func (se *session) kill(connID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := connect(se.in, se.dir, se.root)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()
	_, _ = db.ExecContext(ctx, "KILL QUERY "+strconv.FormatInt(connID, 10))
}

// RowEdit changes rows: inserts one (Values), updates one (Key and
// Values), or deletes some (Keys).
type RowEdit struct {
	Schema string               `json:"schema,omitempty"`
	Table  string               `json:"table"`
	Key    map[string]*string   `json:"key,omitempty"`
	Keys   []map[string]*string `json:"keys,omitempty"`
	Values map[string]*string   `json:"values,omitempty"`
}

// EditRows inserts, updates, or deletes rows by their primary key, in one
// transaction. An update or delete that doesn't match exactly the rows
// asked for changes nothing.
func (s *Service) EditRows(ctx context.Context, serverID, id, op string, e RowEdit) (int64, error) {
	se, err := s.open(ctx, serverID, id, false)
	if err != nil {
		return 0, err
	}
	defer se.Close()
	t, err := se.table(ctx, e.Schema, e.Table)
	if err != nil {
		return 0, err
	}
	if t.Kind != "table" {
		return 0, invalid{"a view's rows can't be changed here"}
	}
	if op != "insert" && len(t.PrimaryKey) == 0 {
		return 0, invalid{"the table has no primary key, so there's no safe way to say which row; change it with SQL"}
	}
	var args []any
	param := func(v *string) string {
		if v != nil && len(*v) > maxEditValue {
			v = nil
		}
		if v == nil {
			args = append(args, nil)
		} else {
			args = append(args, *v)
		}
		if se.engine == Postgres {
			return "$" + strconv.Itoa(len(args))
		}
		return "?"
	}
	checkCols := func(m map[string]*string) error {
		for c, v := range m {
			if _, ok := t.column(c); !ok {
				return invalid{fmt.Sprintf("there's no column %s", c)}
			}
			if v != nil && len(*v) > maxEditValue {
				return invalid{"a value is over 1 MB"}
			}
		}
		return nil
	}
	keyWhere := func(k map[string]*string) (string, error) {
		if len(k) != len(t.PrimaryKey) {
			return "", invalid{"say the row by its whole primary key"}
		}
		var parts []string
		for _, c := range t.PrimaryKey {
			v, ok := k[c]
			if !ok || v == nil {
				return "", invalid{"say the row by its whole primary key"}
			}
			parts = append(parts, se.quote(c)+" = "+param(v))
		}
		return strings.Join(parts, " AND "), nil
	}
	sortedCols := func(m map[string]*string) []string {
		cols := make([]string, 0, len(m))
		for c := range m {
			cols = append(cols, c)
		}
		slices.Sort(cols)
		return cols
	}

	var stmts []string
	var stmtArgs [][]any
	var want []int64
	switch op {
	case "insert":
		if err := checkCols(e.Values); err != nil {
			return 0, err
		}
		cols := sortedCols(e.Values)
		quoted, vals := make([]string, len(cols)), make([]string, len(cols))
		for i, c := range cols {
			quoted[i], vals[i] = se.quote(c), param(e.Values[c])
		}
		q := "INSERT INTO " + se.tableRef(t)
		if len(cols) == 0 {
			if se.engine == Postgres {
				q += " DEFAULT VALUES"
			} else {
				q += " () VALUES ()"
			}
		} else {
			q += " (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ")"
		}
		stmts, stmtArgs, want = []string{q}, [][]any{args}, []int64{1}
	case "update":
		if err := checkCols(e.Values); err != nil {
			return 0, err
		}
		if len(e.Values) == 0 {
			return 0, invalid{"there's nothing to change"}
		}
		var sets []string
		for _, c := range sortedCols(e.Values) {
			sets = append(sets, se.quote(c)+" = "+param(e.Values[c]))
		}
		where, err := keyWhere(e.Key)
		if err != nil {
			return 0, err
		}
		stmts = []string{"UPDATE " + se.tableRef(t) + " SET " + strings.Join(sets, ", ") + " WHERE " + where}
		stmtArgs, want = [][]any{args}, []int64{1}
	case "delete":
		if len(e.Keys) == 0 || len(e.Keys) > MaxRows {
			return 0, invalid{fmt.Sprintf("delete 1 to %d rows at a time", MaxRows)}
		}
		for _, k := range e.Keys {
			args = nil
			where, err := keyWhere(k)
			if err != nil {
				return 0, err
			}
			stmts = append(stmts, "DELETE FROM "+se.tableRef(t)+" WHERE "+where)
			stmtArgs = append(stmtArgs, args)
			want = append(want, 1)
		}
	default:
		return 0, invalid{"insert, update, or delete"}
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout*time.Second)
	defer cancel()
	var total int64
	if se.pg != nil {
		tx, err := se.pg.Begin(ctx)
		if err != nil {
			return 0, err
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		for i, q := range stmts {
			tag, err := tx.Exec(ctx, q, stmtArgs[i]...)
			if err != nil {
				return 0, err
			}
			if op != "insert" && tag.RowsAffected() != want[i] {
				return 0, invalid{"the row isn't there any more, or the key matched more than one; nothing was changed"}
			}
			total += tag.RowsAffected()
		}
		return total, tx.Commit(ctx)
	}
	tx, err := se.my.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for i, q := range stmts {
		r, err := tx.ExecContext(ctx, q, stmtArgs[i]...)
		if err != nil {
			return 0, err
		}
		n, _ := r.RowsAffected()
		// MariaDB and MySQL count an update that changes nothing as 0.
		if op == "delete" && n != want[i] {
			return 0, invalid{"the row isn't there any more; nothing was deleted"}
		}
		if op == "update" && n > 1 {
			return 0, invalid{"the key matched more than one row; nothing was changed"}
		}
		total += n
	}
	return total, tx.Commit()
}

// Process is a connection to the database.
type Process struct {
	ID      int64  `json:"id"`
	User    string `json:"user"`
	State   string `json:"state"`
	Seconds int64  `json:"seconds"`
	Query   string `json:"query"`
}

// Processes lists the connections to a database its user can see: its own.
func (s *Service) Processes(ctx context.Context, serverID, id string) ([]Process, error) {
	se, err := s.open(ctx, serverID, id, false)
	if err != nil {
		return nil, err
	}
	defer se.Close()
	q := `SELECT ID, USER, COALESCE(STATE, COMMAND), TIME, COALESCE(INFO, '') FROM information_schema.PROCESSLIST
WHERE DB = DATABASE() AND ID <> CONNECTION_ID() ORDER BY TIME DESC`
	if se.engine == Postgres {
		q = `SELECT pid, usename, COALESCE(state, ''), COALESCE(EXTRACT(EPOCH FROM now() - query_start)::bigint, 0), COALESCE(query, '')
FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() ORDER BY query_start NULLS LAST`
	}
	rows, err := se.textRows(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]Process, 0, len(rows))
	for _, r := range rows {
		p := Process{ID: num(r[0]), User: str(r[1]), State: str(r[2]), Seconds: num(r[3]), Query: str(r[4])}
		if len(p.Query) > maxCell {
			p.Query = strings.ToValidUTF8(p.Query[:maxCell], "")
		}
		out = append(out, p)
	}
	return out, nil
}

// KillQuery stops one of the database's running queries.
func (s *Service) KillQuery(ctx context.Context, serverID, id string, process int64) error {
	se, err := s.open(ctx, serverID, id, false)
	if err != nil {
		return err
	}
	defer se.Close()
	if se.pg != nil {
		var ok bool
		if err := se.pg.QueryRow(ctx, "SELECT pg_cancel_backend($1)", process).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return invalid{"that query isn't running any more"}
		}
		return nil
	}
	_, err = se.my.ExecContext(ctx, "KILL QUERY "+strconv.FormatInt(process, 10))
	return err
}
