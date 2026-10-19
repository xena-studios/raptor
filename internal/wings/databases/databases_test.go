package databases

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSealer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k", "databases.key")
	s, err := loadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.seal("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	again, err := loadKey(path) // the same key, read back
	if err != nil {
		t.Fatal(err)
	}
	if got, err := again.open(sealed); err != nil || got != "hunter2" {
		t.Fatalf("got %q %v", got, err)
	}
	other, _ := loadKey(filepath.Join(t.TempDir(), "other.key"))
	if _, err := other.open(sealed); err == nil {
		t.Error("opened with another key")
	}
}

func TestPassword(t *testing.T) {
	p, err := password()
	if err != nil || len(p) != 32 || checkPassword(p) != nil {
		t.Fatalf("password %q %v", p, err)
	}
	if checkPassword("a'b") == nil || checkNames("x`y") == nil || checkNames("1abc") == nil {
		t.Error("unsafe input accepted")
	}
}

func TestLookup(t *testing.T) {
	if v, err := lookup(Postgres, ""); err != nil || v.Version != "17" {
		t.Errorf("default postgres: %+v %v", v, err)
	}
	if v, err := lookup(MariaDB, "10.11"); err != nil || !strings.HasPrefix(v.Image, "mariadb:10.11@sha256:") {
		t.Errorf("mariadb 10.11: %+v %v", v, err)
	}
	if _, err := lookup(MySQL, "5.7"); !errors.Is(err, ErrInvalid) {
		t.Errorf("mysql 5.7: %v", err)
	}
	for _, e := range []string{MariaDB, MySQL, Postgres} {
		n := 0
		for _, v := range Versions {
			if v.Engine == e && v.Default {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s has %d defaults", e, n)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := InstanceInput{Name: "Community A MariaDB", Limits: Limits{MemoryMiB: 512}}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []InstanceInput{
		{Name: "", Limits: Limits{MemoryMiB: 512}},
		{Name: "x", Limits: Limits{MemoryMiB: 128}},
		{Name: "x", Limits: Limits{MemoryMiB: 512, DiskMiB: 100}},
		{Name: "x", Limits: Limits{MemoryMiB: 512}, Settings: Settings{MaxConnections: 5}},
		{Name: "x", Limits: Limits{MemoryMiB: 512}, Settings: Settings{Timezone: "Mars/Olympus"}},
		{Name: "x", Limits: Limits{MemoryMiB: 512}, Networks: []string{"a", "a"}},
	} {
		if err := bad.validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestNames(t *testing.T) {
	if got := dbName("0199a3b2-7c4d-7e8f-9a0b-1c2d3e4f5a6b", "luckperms"); got != "s1c2d3e4f5a6b_luckperms" {
		t.Errorf("dbName %q", got)
	}
	if got := hostName("0199a3b2-7c4d", "Community A MariaDB"); got != "community-a-mariadb.db.raptor" {
		t.Errorf("hostName %q", got)
	}
	if got := hostName("0199a3b2-7c4d", "!!!"); got != "7c4d.db.raptor" {
		t.Errorf("hostName of no letters %q", got)
	}
	if !labelRe.MatchString("luck_perms2") || labelRe.MatchString("Luck") || labelRe.MatchString(strings.Repeat("a", 19)) {
		t.Error("labels")
	}
	if n := len(dbName("0199a3b2-7c4d-7e8f-9a0b-1c2d3e4f5a6b", strings.Repeat("a", 18))); n > 32 {
		t.Errorf("a database's user is %d characters, over MySQL's 32", n)
	}
}

func TestShares(t *testing.T) {
	for _, c := range []struct {
		server, instance []string
		want             bool
	}{
		{nil, nil, true},
		{[]string{"a"}, nil, false},
		{nil, []string{"a"}, false},
		{[]string{"a", "b"}, []string{"b"}, true},
		{[]string{"a"}, []string{"b"}, false},
	} {
		if got := shares(c.server, c.instance); got != c.want {
			t.Errorf("shares(%v, %v) = %v", c.server, c.instance, got)
		}
	}
}

func TestServiceSpec(t *testing.T) {
	in := Instance{ID: "0199a3b2-x", Name: "DB", Engine: MySQL, Image: "mysql:8.4@x", Limits: Limits{MemoryMiB: 512}}
	s := serviceSpec(in, "/v/databases/id", "rootpw", "Europe/Berlin")
	for _, want := range []string{"MYSQL_ROOT_PASSWORD=rootpw", "MYSQL_ROOT_HOST=localhost", "TZ=Europe/Berlin"} {
		if !slices.Contains(s.Env, want) {
			t.Errorf("env missing %q: %v", want, s.Env)
		}
	}
	for _, want := range []string{"--innodb-buffer-pool-size=256M", "--performance-schema=OFF", "--secure-file-priv=NULL", "--max-connections=100"} {
		if !slices.Contains(s.Cmd, want) {
			t.Errorf("cmd missing %q: %v", want, s.Cmd)
		}
	}
	if s.Mounts[1].Target != "/var/run/mysqld" {
		t.Errorf("mysql socket at %s", s.Mounts[1].Target)
	}

	pg := serviceSpec(Instance{ID: "x", Engine: Postgres, Limits: Limits{MemoryMiB: 1024}, Settings: Settings{MaxConnections: 50}}, "/d", "pw", "")
	got := strings.Join(pg.Cmd, " ")
	for _, want := range []string{"max_connections=50", "shared_buffers=256MB", "hba_file=/etc/raptor/pg_hba.conf"} {
		if !strings.Contains(got, want) {
			t.Errorf("postgres cmd missing %q: %s", want, got)
		}
	}
	if !pg.Mounts[2].ReadOnly {
		t.Error("pg_hba.conf's folder is writable by the engine")
	}
}

func TestLastLine(t *testing.T) {
	psql := "psql:<stdin>:1: ERROR:  relation \"nowhere\" does not exist\nLINE 1: INSERT INTO nowhere VALUES (1);\n                    ^"
	if got := lastLine(psql); got != `psql:<stdin>:1: ERROR:  relation "nowhere" does not exist` {
		t.Errorf("psql: %q", got)
	}
	if got := lastLine("ERROR 1146 (42S02) at line 1: Table 'x.nowhere' doesn't exist\n"); !strings.Contains(got, "nowhere") {
		t.Errorf("mariadb: %q", got)
	}
	if got := lastLine(""); got != "no reason given" {
		t.Errorf("empty: %q", got)
	}
}
