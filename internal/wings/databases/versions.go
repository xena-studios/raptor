package databases

import (
	"fmt"
	"slices"
)

// Engines.
const (
	MariaDB  = "mariadb"
	MySQL    = "mysql"
	Postgres = "postgres"
)

// Version is one version line of an engine, pinned to an image digest so a
// version means the same bits on every node. Digests are updated with
// Wings releases.
type Version struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Image   string `json:"image"`
	// Default is the version new instances of the engine get.
	Default bool `json:"default,omitempty"`
}

// Versions are the versions offered (docs/DATABASES.md#engines-and-versions).
var Versions = []Version{
	{MariaDB, "10.6", "mariadb:10.6@sha256:88ac718dafee1e8fb415233c04f314f3c3866af99177ce25d436106c62090461", false},
	{MariaDB, "10.11", "mariadb:10.11@sha256:0d0945c57f75b3a3b9ebe71aedaaad005fa5305054e8fca3d2e42a8c92b43b5e", false},
	{MariaDB, "11.4", "mariadb:11.4@sha256:250e1609e93c0e6aecf64bfbab2d84d5e89d2481540fe77447ff6c855c3b1873", false},
	{MariaDB, "11.8", "mariadb:11.8@sha256:f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa", true},
	{MySQL, "8.0", "mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b", false},
	{MySQL, "8.4", "mysql:8.4@sha256:24989179f58856db763f418010c1169fecc944e3ec1ea7e921572d83eb070f70", true},
	{Postgres, "14", "postgres:14@sha256:14bfab572eec6abf65892e1db7c3ba8d41b2a2855c1b143664907d6e146eb6e1", false},
	{Postgres, "15", "postgres:15@sha256:c961aa287d8698297cb26cdfadfbe9fd2cbaf77e53cfffe9636e8d8a1e4d842c", false},
	{Postgres, "16", "postgres:16@sha256:ca0bd484cb98bf4b24eb1010e73fb3fcbd6714d240fbc1a10eea5b7dbecb641d", false},
	{Postgres, "17", "postgres:17@sha256:2d2b8998d31037bf721cfdf764d76ba74171b4fab3431b7f72c27c56ddbdf9e3", true},
	{Postgres, "18", "postgres:18@sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336", false},
}

// lookup returns an engine's version; "" is its default.
func lookup(engine, version string) (Version, error) {
	i := slices.IndexFunc(Versions, func(v Version) bool {
		return v.Engine == engine && (v.Version == version || (version == "" && v.Default))
	})
	if i < 0 {
		return Version{}, invalid{fmt.Sprintf("%s %s isn't offered: pick one of the versions listed", engineName(engine), version)}
	}
	return Versions[i], nil
}

func engineName(e string) string {
	switch e {
	case MariaDB:
		return "MariaDB"
	case MySQL:
		return "MySQL"
	case Postgres:
		return "PostgreSQL"
	}
	return e
}

// port is an engine's port on its networks.
func port(engine string) int {
	if engine == Postgres {
		return 5432
	}
	return 3306
}
