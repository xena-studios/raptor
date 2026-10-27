package databases

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Advanced settings (docs/DATABASES.md#settings): any engine setting on a
// list of what's safe to set, as name = value lines. Nothing on the list
// names a file or a folder, loads code (plugins, modules, libraries),
// touches authentication, TLS, or where the engine listens, or is one of
// the settings Wings sets for its own reasons (the socket, secure-file-priv,
// pg_hba.conf). Values are numbers, sizes, durations, or words from a
// fixed set, so nothing can carry a path or a second option in.
//
// They go on the engine's command line after Wings' own, where the last
// one wins, so a line can change what Wings derives from the memory limit
// (the buffer pool, shared_buffers). Not a file: PostgreSQL can't include
// one from its command line (its postgresql.conf is in the data folder its
// image makes), and the container is made again whenever its command
// changes, so a change takes effect exactly when the instance restarts.
//
// The web app checks the same rules from advanced.json in web/src/lib,
// which TestAdvancedRulesForWeb keeps equal to these.

// Kinds of value.
const (
	kindInt      = "int"      // a whole number
	kindReal     = "real"     // a decimal number
	kindBool     = "bool"     // on or off, written as the engine likes
	kindEnum     = "enum"     // one of Values
	kindSet      = "set"      // a comma-separated list of Values, or none
	kindSize     = "size"     // a number of bytes, with the engine's units
	kindDuration = "duration" // PostgreSQL's: a number with ms, s, min, h, or d
	kindPattern  = "pattern"  // characters from Chars
)

// Rule is what one setting may be.
type Rule struct {
	Kind string `json:"kind"`
	// Min and Max bound numbers, sizes (bytes), and durations (ms).
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	// Memory: a size can't be more than the instance's memory limit.
	Memory bool `json:"memory,omitempty"`
	// Values are an enum's or a set's words.
	Values []string `json:"values,omitempty"`
	// Chars are the characters a pattern may have.
	Chars string `json:"chars,omitempty"`
	// Field: it's a field in the app, not a line (the field's name).
	Field string `json:"field,omitempty"`
}

// Dialect is how an engine writes names, booleans, and units.
type Dialect struct {
	// Separator is the character in names ("_" or "-"); the other is
	// taken too.
	Separator string `json:"separator"`
	// True and False are how a boolean is written.
	True  string `json:"true"`
	False string `json:"false"`
	// Units are size suffixes, in bytes; Bare: a size may have none.
	Units map[string]float64 `json:"units"`
	Bare  bool               `json:"bare"`
	// CaseUnits: units are matched exactly (PostgreSQL's "MB", not "mb").
	CaseUnits bool `json:"case_units,omitempty"`
}

// AdvancedEngine is one engine's rules.
type AdvancedEngine struct {
	Dialect Dialect         `json:"dialect"`
	Rules   map[string]Rule `json:"rules"`
}

// Bounds of the text.
const (
	MaxAdvancedBytes = 4096
	MaxAdvancedLines = 64
)

const (
	kib = 1024.0
	mib = 1024 * kib
	gib = 1024 * mib
	tib = 1024 * gib
)

func intRule(lo, hi float64) Rule  { return Rule{Kind: kindInt, Min: lo, Max: hi} }
func realRule(lo, hi float64) Rule { return Rule{Kind: kindReal, Min: lo, Max: hi} }
func sizeRule(lo, hi float64) Rule { return Rule{Kind: kindSize, Min: lo, Max: hi} }
func memRule(lo float64) Rule      { return Rule{Kind: kindSize, Min: lo, Max: tib, Memory: true} }
func durRule(lo, hi float64) Rule  { return Rule{Kind: kindDuration, Min: lo, Max: hi} }
func enumRule(v ...string) Rule    { return Rule{Kind: kindEnum, Values: v} }
func setRule(v ...string) Rule     { return Rule{Kind: kindSet, Values: v} }

var boolRule = Rule{Kind: kindBool}

const year = 365 * 24 * 3600.0

var mysqlDialect = Dialect{
	Separator: "_", True: "ON", False: "OFF",
	Units: map[string]float64{"K": kib, "M": mib, "G": gib}, Bare: true,
}

var sqlModes = []string{
	"ALLOW_INVALID_DATES", "ANSI", "ANSI_QUOTES", "ERROR_FOR_DIVISION_BY_ZERO", "HIGH_NOT_PRECEDENCE",
	"IGNORE_SPACE", "NO_AUTO_VALUE_ON_ZERO", "NO_BACKSLASH_ESCAPES", "NO_DIR_IN_CREATE",
	"NO_ENGINE_SUBSTITUTION", "NO_UNSIGNED_SUBTRACTION", "NO_ZERO_DATE", "NO_ZERO_IN_DATE",
	"ONLY_FULL_GROUP_BY", "PAD_CHAR_TO_FULL_LENGTH", "PIPES_AS_CONCAT", "REAL_AS_FLOAT",
	"STRICT_ALL_TABLES", "STRICT_TRANS_TABLES", "TIME_TRUNCATE_FRACTIONAL", "TRADITIONAL",
}

// mysqlRules are MariaDB's and MySQL's, with their own added.
func mysqlRules(engine string) map[string]Rule {
	r := map[string]Rule{
		"innodb_buffer_pool_size":         memRule(5 * mib),
		"innodb_log_buffer_size":          sizeRule(256*kib, gib),
		"innodb_flush_log_at_trx_commit":  enumRule("0", "1", "2"),
		"innodb_io_capacity":              intRule(100, 100000),
		"innodb_io_capacity_max":          intRule(100, 200000),
		"innodb_lock_wait_timeout":        intRule(1, 3600),
		"innodb_print_all_deadlocks":      boolRule,
		"innodb_ft_min_token_size":        intRule(0, 16),
		"max_allowed_packet":              sizeRule(kib, gib),
		"wait_timeout":                    intRule(1, year),
		"interactive_timeout":             intRule(1, year),
		"net_read_timeout":                intRule(1, 3600),
		"net_write_timeout":               intRule(1, 3600),
		"connect_timeout":                 intRule(2, 3600),
		"table_open_cache":                intRule(1, 524288),
		"table_definition_cache":          intRule(400, 524288),
		"thread_cache_size":               intRule(0, 16384),
		"tmp_table_size":                  memRule(kib),
		"max_heap_table_size":             memRule(16 * kib),
		"sort_buffer_size":                sizeRule(32*kib, gib),
		"join_buffer_size":                sizeRule(128, gib),
		"read_buffer_size":                sizeRule(8*kib, gib),
		"read_rnd_buffer_size":            sizeRule(kib, gib),
		"slow_query_log":                  boolRule,
		"long_query_time":                 realRule(0, 3600),
		"log_queries_not_using_indexes":   boolRule,
		"sql_mode":                        setRule(sqlModes...),
		"character_set_server":            enumRule("utf8mb4", "utf8mb3", "utf8", "latin1", "ascii", "binary"),
		"collation_server":                {Kind: kindPattern, Chars: "abcdefghijklmnopqrstuvwxyz0123456789_"},
		"transaction_isolation":           enumRule("READ-UNCOMMITTED", "READ-COMMITTED", "REPEATABLE-READ", "SERIALIZABLE"),
		"group_concat_max_len":            intRule(4, 4294967295),
		"max_connect_errors":              intRule(1, 4294967295),
		"max_user_connections":            intRule(0, 4294967295),
		"performance_schema":              boolRule,
		"event_scheduler":                 enumRule("ON", "OFF", "DISABLED"),
		"explicit_defaults_for_timestamp": boolRule,
		"default_storage_engine":          enumRule("InnoDB", "MyISAM"),
		"ft_min_word_len":                 intRule(1, 84),
		"auto_increment_increment":        intRule(1, 65535),
		"auto_increment_offset":           intRule(1, 65535),
		"max_sp_recursion_depth":          intRule(0, 255),
		"max_prepared_stmt_count":         intRule(0, 1048576),
		"max_connections":                 {Field: "Max connections"},
		"default_time_zone":               {Field: "Time zone"},
	}
	if engine == MariaDB {
		r["query_cache_type"] = enumRule("0", "1", "2", "OFF", "ON", "DEMAND")
		r["query_cache_size"] = memRule(0)
		r["aria_pagecache_buffer_size"] = memRule(128 * kib)
		r["innodb_log_file_size"] = sizeRule(4*mib, 16*gib)
	} else {
		r["innodb_redo_log_capacity"] = sizeRule(8*mib, 128*gib)
		r["binlog_expire_logs_seconds"] = intRule(0, 4294967295)
	}
	return r
}

var postgresDialect = Dialect{
	Separator: "_", True: "on", False: "off",
	Units: map[string]float64{"B": 1, "kB": kib, "MB": mib, "GB": gib, "TB": tib}, CaseUnits: true,
}

// Durations are in milliseconds.
var postgresRules = map[string]Rule{
	"shared_buffers":                      memRule(128 * kib),
	"effective_cache_size":                sizeRule(8*kib, 64*tib),
	"work_mem":                            memRule(64 * kib),
	"maintenance_work_mem":                memRule(mib),
	"temp_buffers":                        sizeRule(800*kib, gib),
	"wal_buffers":                         sizeRule(32*kib, 256*mib),
	"random_page_cost":                    realRule(0, 1000),
	"seq_page_cost":                       realRule(0, 1000),
	"effective_io_concurrency":            intRule(0, 1000),
	"max_wal_size":                        sizeRule(2*mib, tib),
	"min_wal_size":                        sizeRule(2*mib, tib),
	"checkpoint_timeout":                  durRule(30_000, 86_400_000),
	"checkpoint_completion_target":        realRule(0, 1),
	"synchronous_commit":                  enumRule("on", "off", "local", "remote_write", "remote_apply"),
	"statement_timeout":                   durRule(0, math.MaxInt32),
	"lock_timeout":                        durRule(0, math.MaxInt32),
	"idle_in_transaction_session_timeout": durRule(0, math.MaxInt32),
	"idle_session_timeout":                durRule(0, math.MaxInt32),
	"log_min_duration_statement":          durRule(-1, math.MaxInt32),
	"log_lock_waits":                      boolRule,
	"log_checkpoints":                     boolRule,
	"log_autovacuum_min_duration":         durRule(-1, math.MaxInt32),
	"autovacuum_vacuum_scale_factor":      realRule(0, 100),
	"autovacuum_analyze_scale_factor":     realRule(0, 100),
	"autovacuum_naptime":                  durRule(1000, 86_400_000),
	"autovacuum_max_workers":              intRule(1, 64),
	"autovacuum_vacuum_cost_limit":        intRule(-1, 10000),
	"default_statistics_target":           intRule(1, 10000),
	"jit":                                 boolRule,
	"max_parallel_workers_per_gather":     intRule(0, 64),
	"max_parallel_workers":                intRule(0, 64),
	"max_worker_processes":                intRule(0, 64),
	"max_locks_per_transaction":           intRule(10, 10000),
	"max_prepared_transactions":           intRule(0, 1000),
	"track_io_timing":                     boolRule,
	"max_connections":                     {Field: "Max connections"},
	"timezone":                            {Field: "Time zone"},
	"log_timezone":                        {Field: "Time zone"},
}

var redisDialect = Dialect{
	Separator: "-", True: "yes", False: "no",
	Units: map[string]float64{"k": 1000, "kb": kib, "m": 1000 * 1000, "mb": mib, "g": 1000 * 1000 * 1000, "gb": gib}, Bare: true,
}

var redisRules = map[string]Rule{
	"maxmemory-policy": enumRule("noeviction", "allkeys-lru", "allkeys-lfu", "allkeys-random",
		"volatile-lru", "volatile-lfu", "volatile-random", "volatile-ttl"),
	"maxmemory-samples":           intRule(1, 64),
	"timeout":                     intRule(0, year),
	"tcp-keepalive":               intRule(0, year),
	"hz":                          intRule(1, 500),
	"dynamic-hz":                  boolRule,
	"notify-keyspace-events":      {Kind: kindPattern, Chars: "KEg$lshzxetmdnA"},
	"slowlog-log-slower-than":     intRule(-1, 1e12),
	"slowlog-max-len":             intRule(0, 100000),
	"latency-monitor-threshold":   intRule(0, 1e12),
	"lazyfree-lazy-eviction":      boolRule,
	"lazyfree-lazy-expire":        boolRule,
	"lazyfree-lazy-server-del":    boolRule,
	"lazyfree-lazy-user-del":      boolRule,
	"lazyfree-lazy-user-flush":    boolRule,
	"appendfsync":                 enumRule("always", "everysec", "no"),
	"auto-aof-rewrite-percentage": intRule(0, 1000),
	"auto-aof-rewrite-min-size":   sizeRule(0, 64*gib),
	"activedefrag":                boolRule,
	"hash-max-listpack-entries":   intRule(0, 1e6),
	"hash-max-listpack-value":     intRule(0, 1e6),
	"set-max-intset-entries":      intRule(0, 1e6),
	"set-max-listpack-entries":    intRule(0, 1e6),
	"set-max-listpack-value":      intRule(0, 1e6),
	"zset-max-listpack-entries":   intRule(0, 1e6),
	"zset-max-listpack-value":     intRule(0, 1e6),
	"list-max-listpack-size":      intRule(-5, 1e6),
	"list-compress-depth":         intRule(0, 1e6),
	"stream-node-max-bytes":       sizeRule(0, gib),
	"stream-node-max-entries":     intRule(0, 1e9),
	"lfu-log-factor":              intRule(0, 1e6),
	"lfu-decay-time":              intRule(0, 1e6),
	"busy-reply-threshold":        intRule(0, 1e9),
	"io-threads":                  intRule(1, 16),
	"maxclients":                  {Field: "Max connections"},
}

// AdvancedRules are every engine's rules; an engine with none has no
// advanced settings (MongoDB: its options are YAML and server parameters,
// and few are safe and worth having; until someone asks).
func AdvancedRules() map[string]AdvancedEngine {
	return map[string]AdvancedEngine{
		MariaDB:  {mysqlDialect, mysqlRules(MariaDB)},
		MySQL:    {mysqlDialect, mysqlRules(MySQL)},
		Postgres: {postgresDialect, postgresRules},
		Redis:    {redisDialect, redisRules},
	}
}

// advancedRules is AdvancedRules, made once.
var advancedRules = AdvancedRules()

// Setting is one advanced setting, its name and value as the engine
// takes them.
type Setting struct {
	Name, Value string
}

// AdvancedError is a line that isn't right.
type AdvancedError struct {
	Line int // from 1
	Msg  string
}

func (e AdvancedError) Error() string {
	return fmt.Sprintf("advanced settings, line %d: %s", e.Line, e.Msg)
}
func (e AdvancedError) Is(target error) bool { return target == ErrInvalid }

var settingName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// valueChars are all a value may have: no spaces, quotes, slashes,
// or anything a config line or a command line treats specially.
func valueChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.,+-$", r)
}

// parseAdvanced checks an instance's advanced settings for an engine with
// memMiB of memory, and returns them as the engine takes them. Lines are
// name = value; blank ones and ones starting with # are skipped.
func parseAdvanced(engine, text string, memMiB int64) ([]Setting, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	eng, ok := advancedRules[engine]
	if !ok {
		return nil, invalid{fmt.Sprintf("%s has no advanced settings", engineName(engine))}
	}
	if len(text) > MaxAdvancedBytes {
		return nil, invalid{fmt.Sprintf("advanced settings are at most %d characters", MaxAdvancedBytes)}
	}
	var out []Setting
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(out) == MaxAdvancedLines {
			return nil, AdvancedError{n, fmt.Sprintf("at most %d settings", MaxAdvancedLines)}
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, AdvancedError{n, "write it as name = value"}
		}
		name, value = strings.TrimSpace(name), unquote(strings.TrimSpace(value))
		if !settingName.MatchString(name) {
			return nil, AdvancedError{n, fmt.Sprintf("%q isn't a setting's name", name)}
		}
		name = normalName(eng.Dialect, name)
		rule, ok := eng.Rules[name]
		if !ok {
			return nil, AdvancedError{n, fmt.Sprintf("%s isn't one of the settings that can be changed here", name)}
		}
		if rule.Field != "" {
			return nil, AdvancedError{n, fmt.Sprintf("%s is the %s field", name, rule.Field)}
		}
		if slices.ContainsFunc(out, func(s Setting) bool { return s.Name == name }) {
			return nil, AdvancedError{n, fmt.Sprintf("%s is set twice", name)}
		}
		if rule.Kind == kindSet {
			value = strings.Join(strings.Fields(strings.ReplaceAll(value, ",", " , ")), "")
		}
		if strings.IndexFunc(value, func(r rune) bool { return !valueChar(r) }) >= 0 {
			return nil, AdvancedError{n, "a value is letters, digits, and . , _ - + $ only"}
		}
		v, err := checkValue(eng.Dialect, rule, value, memMiB)
		if err != nil {
			return nil, AdvancedError{n, name + ": " + err.Error()}
		}
		out = append(out, Setting{name, v})
	}
	return out, nil
}

// unquote takes off one pair of quotes around a value, which configs
// often have.
func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}

func normalName(d Dialect, name string) string {
	name = strings.ToLower(name)
	if d.Separator == "-" {
		return strings.ReplaceAll(name, "_", "-")
	}
	return strings.ReplaceAll(name, "-", "_")
}

// checkValue checks a value against its rule and returns it as the engine
// takes it.
func checkValue(d Dialect, r Rule, v string, memMiB int64) (string, error) {
	switch r.Kind {
	case kindBool:
		switch strings.ToLower(v) {
		case "on", "true", "yes", "1":
			return d.True, nil
		case "off", "false", "no", "0":
			return d.False, nil
		}
		return "", fmt.Errorf("%s or %s", d.True, d.False)
	case kindEnum:
		for _, x := range r.Values {
			if strings.EqualFold(x, v) {
				return x, nil
			}
		}
		return "", fmt.Errorf("one of %s", strings.Join(r.Values, ", "))
	case kindSet:
		if v == "" {
			return "", nil
		}
		var out []string
		for p := range strings.SplitSeq(v, ",") {
			p = strings.TrimSpace(p)
			i := slices.IndexFunc(r.Values, func(x string) bool { return strings.EqualFold(x, p) })
			if i < 0 {
				return "", fmt.Errorf("%q isn't one of %s", p, strings.Join(r.Values, ", "))
			}
			out = append(out, r.Values[i])
		}
		return strings.Join(out, ","), nil
	case kindPattern:
		for _, c := range v {
			if !strings.ContainsRune(r.Chars, c) {
				return "", fmt.Errorf("only the characters %s", r.Chars)
			}
		}
		return v, nil
	case kindInt, kindReal:
		re, what := intRe, "a whole number"
		if r.Kind == kindReal {
			re, what = realRe, "a number"
		}
		f, err := strconv.ParseFloat(v, 64)
		if !re.MatchString(v) || err != nil {
			return "", errors.New(what)
		}
		if f < r.Min || f > r.Max {
			return "", fmt.Errorf("between %s and %s", fnum(r.Min), fnum(r.Max))
		}
		return v, nil
	case kindSize:
		b, err := parseSize(d, v)
		if err != nil {
			return "", err
		}
		limit := r.Max
		if r.Memory && memMiB > 0 {
			limit = min(limit, float64(memMiB)*mib)
		}
		if b < r.Min || b > limit {
			what := "between " + sizeText(r.Min) + " and " + sizeText(limit)
			if limit < r.Max {
				what += ", the memory limit"
			}
			return "", fmt.Errorf("%s", what)
		}
		return v, nil
	case kindDuration:
		ms, err := parseDuration(v)
		if err != nil {
			return "", err
		}
		if ms < r.Min || ms > r.Max {
			return "", fmt.Errorf("between %s and %s", millis(r.Min), millis(r.Max))
		}
		return v, nil
	}
	return "", fmt.Errorf("can't be set")
}

// Numbers: digits, a minus, and a decimal point; no exponents, hex, or
// infinities.
var (
	intRe  = regexp.MustCompile(`^-?[0-9]{1,15}$`)
	realRe = regexp.MustCompile(`^-?([0-9]{1,15}(\.[0-9]{0,15})?|\.[0-9]{1,15})$`)
)

// sizeRe is a size: digits, then a unit or none.
var sizeRe = regexp.MustCompile(`^([0-9]{1,15})([A-Za-z]{0,2})$`)

func parseSize(d Dialect, v string) (float64, error) {
	m := sizeRe.FindStringSubmatch(v)
	if m == nil {
		return 0, fmt.Errorf("a size, like %s", sizeExample(d))
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	if m[2] == "" {
		if !d.Bare {
			return 0, fmt.Errorf("a size with its unit, like %s", sizeExample(d))
		}
		return n, nil
	}
	for u, mul := range d.Units {
		if u == m[2] || !d.CaseUnits && strings.EqualFold(u, m[2]) {
			return n * mul, nil
		}
	}
	return 0, fmt.Errorf("a size, like %s", sizeExample(d))
}

func sizeExample(d Dialect) string {
	switch {
	case d.CaseUnits:
		return "128MB"
	case d.Separator == "-":
		return "64mb"
	}
	return "128M"
}

var durationRe = regexp.MustCompile(`^(-?[0-9]{1,10})(us|ms|s|min|h|d)?$`)

// parseDuration is a PostgreSQL duration in milliseconds: a unit is
// needed but for 0 and -1, which mean off.
func parseDuration(v string) (float64, error) {
	m := durationRe.FindStringSubmatch(v)
	if m == nil {
		return 0, fmt.Errorf("a time, like 30s or 500ms")
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	mul := map[string]float64{"us": 0.001, "ms": 1, "s": 1000, "min": 60_000, "h": 3_600_000, "d": 86_400_000}[m[2]]
	if m[2] == "" {
		if n != 0 && n != -1 {
			return 0, fmt.Errorf("a time with its unit, like 30s or 500ms")
		}
		return n, nil
	}
	if n < 0 {
		return 0, fmt.Errorf("-1 for off, or a time like 30s")
	}
	return n * mul, nil
}

func fnum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func sizeText(b float64) string {
	for _, u := range []struct {
		n string
		m float64
	}{{"TB", tib}, {"GB", gib}, {"MB", mib}, {"kB", kib}} {
		if b >= u.m && b == math.Trunc(b/u.m)*u.m {
			return fnum(b/u.m) + " " + u.n
		}
	}
	if b >= mib {
		return fnum(math.Floor(b/mib)) + " MB"
	}
	return fnum(b) + " bytes"
}

func millis(ms float64) string {
	switch {
	case ms == -1:
		return "-1 (off)"
	case ms >= 86_400_000 && ms == math.Trunc(ms/86_400_000)*86_400_000:
		return fnum(ms/86_400_000) + "d"
	case ms >= 1000 && ms == math.Trunc(ms/1000)*1000:
		return fnum(ms/1000) + "s"
	}
	return fnum(ms) + "ms"
}

// advancedSettings are an instance's advanced settings. Lines that are no
// longer allowed (the list changed in an update) are left out rather
// than keep it from starting.
func advancedSettings(in Instance) []Setting {
	var out []Setting
	for line := range strings.SplitSeq(in.Settings.Advanced, "\n") {
		set, err := parseAdvanced(in.Engine, line, in.Limits.MemoryMiB)
		if err != nil || len(set) == 0 || slices.ContainsFunc(out, func(s Setting) bool { return s.Name == set[0].Name }) {
			continue
		}
		out = append(out, set[0])
	}
	return out
}

// withAdvanced puts an instance's advanced settings on its engine's
// command line, in place of Wings' own for the same setting (the buffer
// pool, the eviction policy), and after the rest.
func withAdvanced(engine string, cmd []string, set []Setting) []string {
	if len(set) == 0 {
		return cmd
	}
	d := advancedRules[engine].Dialect
	ours := func(name string) bool {
		name = normalName(d, name)
		return slices.ContainsFunc(set, func(s Setting) bool { return s.Name == name })
	}
	out := make([]string, 0, len(cmd)+2*len(set))
	for i := 0; i < len(cmd); i++ {
		a := cmd[i]
		switch {
		case engine == Postgres && a == "-c" && i+1 < len(cmd):
			if name, _, _ := strings.Cut(cmd[i+1], "="); ours(name) {
				i++
				continue
			}
		case engine == Redis && strings.HasPrefix(a, "--") && i+1 < len(cmd):
			if ours(a[2:]) {
				i++
				continue
			}
		case (engine == MariaDB || engine == MySQL) && strings.HasPrefix(a, "--"):
			if name, _, ok := strings.Cut(a[2:], "="); ok && ours(name) {
				continue
			}
		}
		out = append(out, a)
	}
	for _, s := range set {
		switch engine {
		case Postgres:
			out = append(out, "-c", s.Name+"="+s.Value)
		case Redis:
			out = append(out, "--"+s.Name, s.Value)
		default:
			out = append(out, "--"+s.Name+"="+s.Value)
		}
	}
	return out
}
