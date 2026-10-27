//go:build e2e

package databases

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// TestAdvancedSettings gives each engine advanced settings and reads them
// back from the running engine: they take effect, they win over what
// Wings derives from the memory limit, a change restarts the instance with
// the new value, and a setting off the list is refused.
func TestAdvancedSettings(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E settings")
	for _, c := range []struct {
		engine        string
		first, second string
		// read returns the values the engine has for names.
		read  func(t *testing.T, e e2e, in Instance) map[string]string
		want1 map[string]string
		want2 map[string]string
	}{
		{
			engine: MariaDB,
			first:  "innodb_buffer_pool_size = 128M\nmax-allowed-packet = 32M\nsql_mode = STRICT_ALL_TABLES",
			second: "max_allowed_packet = 16M",
			read:   readMySQL("@@innodb_buffer_pool_size", "@@max_allowed_packet", "@@sql_mode"),
			want1:  map[string]string{"@@innodb_buffer_pool_size": "134217728", "@@max_allowed_packet": "33554432", "@@sql_mode": "STRICT_ALL_TABLES"},
			// Back to what Wings derives: half of 512 MB.
			want2: map[string]string{"@@innodb_buffer_pool_size": "268435456", "@@max_allowed_packet": "16777216"},
		},
		{
			engine: MySQL,
			first:  "slow_query_log = on\nlong_query_time = 0.25\ninnodb_buffer_pool_size = 64M",
			second: "slow_query_log = off",
			read:   readMySQL("@@slow_query_log", "@@GLOBAL.long_query_time", "@@innodb_buffer_pool_size"),
			want1:  map[string]string{"@@slow_query_log": "1", "@@GLOBAL.long_query_time": "0.25", "@@innodb_buffer_pool_size": "67108864"},
			want2:  map[string]string{"@@slow_query_log": "0", "@@innodb_buffer_pool_size": "268435456"},
		},
		{
			engine: Postgres,
			first:  "shared_buffers = 64MB\nwork_mem = '8MB'\nstatement_timeout = 30s\nlog_min_duration_statement = 0",
			second: "work_mem = 16MB",
			read:   readPostgres("shared_buffers", "work_mem", "statement_timeout"),
			want1:  map[string]string{"shared_buffers": "64MB", "work_mem": "8MB", "statement_timeout": "30s"},
			want2:  map[string]string{"shared_buffers": "128MB", "work_mem": "16MB", "statement_timeout": "0"},
		},
		{
			engine: Redis,
			first:  "maxmemory-policy = allkeys-lru\nnotify_keyspace_events = Ex\nhz = 20",
			second: "hz = 15",
			read:   readRedis("maxmemory-policy", "notify-keyspace-events", "hz"),
			want1:  map[string]string{"maxmemory-policy": "allkeys-lru", "notify-keyspace-events": "xE", "hz": "20"},
			want2:  map[string]string{"maxmemory-policy": "noeviction", "notify-keyspace-events": "", "hz": "15"},
		},
	} {
		t.Run(c.engine, func(t *testing.T) {
			input := InstanceInput{
				Name: "E2E settings " + c.engine, Engine: c.engine, Networks: []string{e.net.ID},
				Limits: Limits{MemoryMiB: 512}, Settings: Settings{Advanced: "hba_file = /tmp/x"},
			}
			if _, err := e.svc.Save(ctx, input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("a setting off the list: %v", err)
			}
			input.Settings.Advanced = c.first
			in, err := e.svc.Save(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
			in = waitRunning(t, e.svc, in.ID)
			if got := c.read(t, e, in); !matches(got, c.want1) {
				t.Errorf("first: %v, want %v", got, c.want1)
			}
			// Wings' own sessions still work, with statement logging on.
			if _, _, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "s"+c.engine[:2]); err != nil {
				t.Fatalf("a database with the settings on: %v", err)
			}

			input.ID, input.Engine, input.Settings.Advanced = in.ID, "", c.second
			if _, err := e.svc.Save(ctx, input); err != nil {
				t.Fatal(err)
			}
			in = waitRunning(t, e.svc, in.ID)
			if got := c.read(t, e, in); !matches(got, c.want2) {
				t.Errorf("after the change: %v, want %v", got, c.want2)
			}
		})
	}
}

func matches(got, want map[string]string) bool {
	for k, v := range want {
		g, err1 := strconv.ParseFloat(got[k], 64)
		w, err2 := strconv.ParseFloat(v, 64)
		if got[k] != v && (err1 != nil || err2 != nil || g != w) {
			return false
		}
	}
	return true
}

func (e e2e) root(t *testing.T, id string) string {
	t.Helper()
	_, r, err := e.svc.instance(context.Background(), e.db.Read, id)
	if err != nil {
		t.Fatal(err)
	}
	root, err := e.svc.sealer.open(r.RootPassword)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readMySQL(vars ...string) func(*testing.T, e2e, Instance) map[string]string {
	return func(t *testing.T, e e2e, in Instance) map[string]string {
		return readSQL(t, e, in, "SELECT "+strings.Join(vars, ", "), vars)
	}
}

func readPostgres(names ...string) func(*testing.T, e2e, Instance) map[string]string {
	return func(t *testing.T, e e2e, in Instance) map[string]string {
		var sel []string
		for _, n := range names {
			sel = append(sel, "current_setting('"+n+"')")
		}
		return readSQL(t, e, in, "SELECT "+strings.Join(sel, ", "), names)
	}
}

// readSQL reads one row as the instance's administrator, through its
// socket.
func readSQL(t *testing.T, e e2e, in Instance, stmt string, names []string) map[string]string {
	t.Helper()
	db, err := connect(in, e.svc.dir(in.ID), e.root(t, in.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	vals := make([]string, len(names))
	ptrs := make([]any, len(names))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := db.QueryRowContext(context.Background(), stmt).Scan(ptrs...); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
	out := map[string]string{}
	for i, n := range names {
		out[n] = vals[i]
	}
	return out
}

func readRedis(names ...string) func(*testing.T, e2e, Instance) map[string]string {
	return func(t *testing.T, e e2e, in Instance) map[string]string {
		t.Helper()
		c := redisClient(e.svc.dir(in.ID), e.root(t, in.ID))
		defer func() { _ = c.Close() }()
		out := map[string]string{}
		for _, n := range names {
			v, err := c.ConfigGet(context.Background(), n).Result()
			if err != nil {
				t.Fatalf("CONFIG GET %s: %v", n, err)
			}
			out[n] = v[n]
		}
		return out
	}
}
