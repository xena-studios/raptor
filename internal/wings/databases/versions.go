package databases

import (
	"fmt"
	"slices"
	"strings"
)

// Engines.
const (
	MariaDB  = "mariadb"
	MySQL    = "mysql"
	Postgres = "postgres"
	Redis    = "redis"
	MongoDB  = "mongodb"
	// Valkey is the BSD-licensed fork of Redis 7.2, beside it (DECISIONS
	// #333): the same protocol, commands, and files, so everything Redis
	// does here it does the same way.
	Valkey = "valkey"
)

// isSQL reports whether an engine speaks SQL: the browser, imports, and
// external access are theirs for now.
func isSQL(engine string) bool { return engine == MariaDB || engine == MySQL || engine == Postgres }

// isRedis reports whether an engine is Redis or Valkey.
func isRedis(engine string) bool { return engine == Redis || engine == Valkey }

// Version is one version line of an engine, pinned to an image digest so a
// version means the same bits on every node. Digests are updated with
// Wings releases.
type Version struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Image   string `json:"image"`
	// Default is the version new instances of the engine get.
	Default bool `json:"default,omitempty"`
	// Unavailable says why this node can't run it ("" when it can).
	Unavailable string `json:"unavailable,omitempty"`
}

// Versions are the versions offered (docs/DATABASES.md#engines-and-versions).
var Versions = []Version{
	{MariaDB, "10.6", "mariadb:10.6@sha256:88ac718dafee1e8fb415233c04f314f3c3866af99177ce25d436106c62090461", false, ""},
	{MariaDB, "10.11", "mariadb:10.11@sha256:0d0945c57f75b3a3b9ebe71aedaaad005fa5305054e8fca3d2e42a8c92b43b5e", false, ""},
	{MariaDB, "11.4", "mariadb:11.4@sha256:250e1609e93c0e6aecf64bfbab2d84d5e89d2481540fe77447ff6c855c3b1873", false, ""},
	{MariaDB, "11.8", "mariadb:11.8@sha256:f16d7a344413e519dff518f27bdb221388f84b0d274c97c4fb5f2cb0dc1b34fa", true, ""},
	{MySQL, "8.0", "mysql:8.0@sha256:7dcddc01f13bab2f15cde676d44d01f61fc9f99fe7785e86196dfc07d358ae2b", false, ""},
	{MySQL, "8.4", "mysql:8.4@sha256:24989179f58856db763f418010c1169fecc944e3ec1ea7e921572d83eb070f70", true, ""},
	{Postgres, "14", "postgres:14@sha256:14bfab572eec6abf65892e1db7c3ba8d41b2a2855c1b143664907d6e146eb6e1", false, ""},
	{Postgres, "15", "postgres:15@sha256:c961aa287d8698297cb26cdfadfbe9fd2cbaf77e53cfffe9636e8d8a1e4d842c", false, ""},
	{Postgres, "16", "postgres:16@sha256:ca0bd484cb98bf4b24eb1010e73fb3fcbd6714d240fbc1a10eea5b7dbecb641d", false, ""},
	{Postgres, "17", "postgres:17@sha256:2d2b8998d31037bf721cfdf764d76ba74171b4fab3431b7f72c27c56ddbdf9e3", true, ""},
	{Postgres, "18", "postgres:18@sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336", false, ""},
	{Redis, "7.2", "redis:7.2@sha256:46f15cf64fdef63e483dcda99e74e3d51c017b1175547b6bb874152ee2391aad", false, ""},
	{Redis, "7.4", "redis:7.4@sha256:4fa24486b8bcca8eec45ee0eb166edc674795e53a2b53d1a9ef263eecebaac85", false, ""},
	{Redis, "8.2", "redis:8.2@sha256:47670742d7924adbcdb404d1288b6327815b23141969c0939b8e5d61f78c2634", false, ""},
	{Redis, "8.10", "redis:8.10@sha256:2c2dff791878316e3b083188be0d578423b24813cdda5de0e80ea4f7b3e6bfd6", true, ""},
	{Valkey, "8.0", "valkey/valkey:8.0@sha256:04b9002ae24bbe66687fb025052ed9bcebb54f8da3937338eb718d98d362887f", false, ""},
	{Valkey, "8.1", "valkey/valkey:8.1@sha256:8f836008e0986c36adf50da714c7060cf689dbb335f61fe077939d90fa650809", false, ""},
	{Valkey, "9.0", "valkey/valkey:9.0@sha256:7e1deda01b64ece3167d370ab5be3452d29734feec22cbcbc69b3a64f6859d6d", false, ""},
	{Valkey, "9.1", "valkey/valkey:9.1@sha256:3add1a336a920ea54770a001ab6feb3da5fceea96a40373a3a2cd469b815f0b7", true, ""},
	// MongoDB's major releases only (its x.1–x.3 rapid releases aren't
	// for production); 8.0 and up need a CPU from 2016 or later (ARMv8.2,
	// or x86 with AVX).
	{MongoDB, "7.0", "mongo:7.0@sha256:c53852c00c23ca9f23fc39690350ffa9a80e3642fe8f96879726d8da0abe80a7", false, ""},
	{MongoDB, "8.0", "mongo:8.0@sha256:4a4ab5d4834207389e0d7270fac988e21ffedccecba77991a552f755df229907", true, ""},
	{MongoDB, "9.0", "mongo:9.0@sha256:dc28ed5a9d0fc616903c3f463bb1e53b92b49f441721b91926e87d90bf957ce8", false, ""},
}

// unavailable says why a version can't run on a node with this kernel
// release ("6.19.8-azure"), or "" if it can. MongoDB 8.0 and later refuse
// to start on Linux 6.19 and newer: their memory allocator breaks the
// kernel's rseq rules, and they exit rather than crash (MongoDB's
// SERVER-121912, unfixed as of October 2026). 7.0 runs.
func unavailable(v Version, kernel string) string {
	if v.Engine != MongoDB || parseVersion(v.Version)[0] < 8 {
		return ""
	}
	if k := parseVersion(strings.SplitN(kernel, "-", 2)[0]); len(k) >= 2 && (k[0] > 6 || k[0] == 6 && k[1] >= 19) {
		return fmt.Sprintf("MongoDB %s doesn't start on Linux 6.19 or newer, and this node runs %s; use 7.0 until MongoDB fixes it", v.Version, kernel)
	}
	return ""
}

// offered is the versions with what this node can't run marked, and an
// engine's default moved to its newest it can when the usual one is out.
func offered(kernel string) []Version {
	out := slices.Clone(Versions)
	for i := range out {
		out[i].Unavailable = unavailable(out[i], kernel)
	}
	for i := range out {
		if !out[i].Default || out[i].Unavailable == "" {
			continue
		}
		out[i].Default = false
		for j := len(out) - 1; j >= 0; j-- {
			if out[j].Engine == out[i].Engine && out[j].Unavailable == "" {
				out[j].Default = true
				break
			}
		}
	}
	return out
}

// lookupOn returns an engine's version this node can run; "" is its
// default here.
func lookupOn(engine, version, kernel string) (Version, error) {
	vs := offered(kernel)
	i := slices.IndexFunc(vs, func(v Version) bool {
		return v.Engine == engine && (v.Version == version || (version == "" && v.Default))
	})
	if i < 0 {
		return lookup(engine, version)
	}
	if vs[i].Unavailable != "" {
		return Version{}, invalid{vs[i].Unavailable}
	}
	return vs[i], nil
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
	case Redis:
		return "Redis"
	case Valkey:
		return "Valkey"
	case MongoDB:
		return "MongoDB"
	}
	return e
}

// port is an engine's port on its networks.
func port(engine string) int {
	switch engine {
	case Postgres:
		return 5432
	case Redis, Valkey:
		return 6379
	case MongoDB:
		return 27017
	}
	return 3306
}
