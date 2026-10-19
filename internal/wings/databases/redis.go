package databases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// Redis (docs/DATABASES.md#redis-and-mongodb): it has no real isolation
// between databases (SELECT is a number, and ACL key patterns depend on
// every client prefixing its keys), so an instance holds one server's
// data. Its users are ACL users in an ACL file in the data folder, which
// Redis saves itself (ACL SAVE): Wings' admin (raptor, every command), the
// server's user (everything but administration), and its read-only user.
// The default user is off. Wings reaches it through its socket.

// adminUser is Wings' user on Redis and MongoDB instances.
const adminUser = "raptor"

func redisSocket(dir string) string { return filepath.Join(runDir(dir), "redis.sock") }
func aclFile(dir string) string     { return filepath.Join(dataDir(dir), "users.acl") }

// Rules for a server's user: everything but administration (CONFIG,
// MODULE, DEBUG, SHUTDOWN, replication…) and the dangerous commands, bar
// the few apps use. The read-only user reads.
const (
	redisUserRules     = "~* &* +@all -@admin -@dangerous +flushdb +flushall +keys +info +sort +sort_ro +swapdb"
	redisReadOnlyRules = "~* &* -@all +@read +@connection -@admin -@dangerous +keys +info"
)

func redisSpec(s *containers.ServiceSpec, in Instance, dir string, maxConn int) {
	mem := in.Limits.MemoryMiB
	// As Redis' own user from the start: Redis 8.4's entrypoint drops root
	// with setpriv, which needs CAP_SETPCAP. Its folders are already its.
	s.User = strconv.Itoa(engineUID) + ":" + strconv.Itoa(engineUID)
	s.Mounts = []containers.Mount{
		{Source: dataDir(dir), Target: "/data"},
		{Source: runDir(dir), Target: "/run/redis"},
	}
	s.Cmd = []string{
		"redis-server",
		"--aclfile", "/data/users.acl",
		"--unixsocket", "/run/redis/redis.sock", "--unixsocketperm", "700",
		"--protected-mode", "no", // the default user is off; every user has a password
		"--appendonly", "yes",
		"--dir", "/data",
		"--maxclients", strconv.Itoa(maxConn),
		// Most of the memory, the rest for Redis itself and its forks for
		// saving; writes fail rather than keys being dropped.
		"--maxmemory", strconv.FormatInt(max(mem*3/4, 64), 10) + "mb",
		"--maxmemory-policy", "noeviction",
	}
}

// writeACL writes the instance's first ACL file, with Wings' user, if it
// has none: from then on Redis keeps it.
func writeACL(dir, root string) error {
	if _, err := os.Stat(aclFile(dir)); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := checkPassword(root); err != nil {
		return err
	}
	body := "user default off\nuser " + adminUser + " on #" + sha(root) + " ~* &* +@all\n"
	tmp := aclFile(dir) + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	if err := os.Chown(tmp, engineUID, engineUID); err != nil && !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return os.Rename(tmp, aclFile(dir))
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func redisClient(dir, root string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Network: "unix", Addr: redisSocket(dir), Username: adminUser, Password: root,
		DialTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, MaxRetries: -1,
		// No CLIENT SETINFO: nothing about the client is worth sending.
		DisableIdentity: true,
	})
}

// redisDo runs commands as Wings, then saves the ACL file if asked.
func redisDo(ctx context.Context, dir, root string, save bool, cmds ...[]any) error {
	c := redisClient(dir, root)
	defer func() { _ = c.Close() }()
	for _, cmd := range cmds {
		if err := c.Do(ctx, cmd...).Err(); err != nil {
			return fmt.Errorf("%v: %w", cmd[0], err)
		}
	}
	if save {
		return c.Do(ctx, "ACL", "SAVE").Err()
	}
	return nil
}

func redisSetUser(user, pass, rules string) []any {
	out := []any{"ACL", "SETUSER", user, "reset", "on", ">" + pass}
	for _, r := range splitRules(rules) {
		out = append(out, r)
	}
	return out
}

func splitRules(s string) []string { return strings.Fields(s) }

func redisCreate(ctx context.Context, dir, root, user, pass string) error {
	return redisDo(ctx, dir, root, true, redisSetUser(user, pass, redisUserRules))
}

// redisDrop deletes the server's user and its data: the instance holds
// only its data.
func redisDrop(ctx context.Context, dir, root, user string) error {
	return redisDo(ctx, dir, root, true, []any{"ACL", "DELUSER", user}, []any{"FLUSHALL"})
}

func redisPassword(ctx context.Context, dir, root, user, pass string) error {
	return redisDo(ctx, dir, root, true, []any{"ACL", "SETUSER", user, "resetpass", ">" + pass})
}

func redisReadOnly(ctx context.Context, dir, root, user, pass string) error {
	return redisDo(ctx, dir, root, true, redisSetUser(user, pass, redisReadOnlyRules))
}

func redisDropUser(ctx context.Context, dir, root, user string) error {
	return redisDo(ctx, dir, root, true, []any{"ACL", "DELUSER", user})
}

func redisPing(ctx context.Context, dir, root string) error {
	return redisDo(ctx, dir, root, false, []any{"PING"})
}

// redisKeys counts the keys in each of the instance's numbered databases,
// for checking a version change.
func redisKeys(ctx context.Context, dir, root string) (map[string]int64, error) {
	c := redisClient(dir, root)
	defer func() { _ = c.Close() }()
	out := map[string]int64{}
	for db := range 16 {
		if err := c.Do(ctx, "SELECT", db).Err(); err != nil {
			break // fewer databases configured
		}
		n, err := c.DBSize(ctx).Result()
		if err != nil {
			return nil, err
		}
		if n > 0 {
			out[fmt.Sprintf("db%d: keys", db)] = n
		}
	}
	return out, nil
}

// redisDump writes a snapshot of the instance's data (an RDB file): Redis
// saves it in the background, and it's read from the data folder.
func redisDump(ctx context.Context, dir, root string, w io.Writer) error {
	c := redisClient(dir, root)
	defer func() { _ = c.Close() }()
	before, err := c.LastSave(ctx).Result()
	if err != nil {
		return err
	}
	if err := c.BgSave(ctx).Err(); err != nil {
		return fmt.Errorf("BGSAVE: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		last, err := c.LastSave(ctx).Result()
		if err != nil {
			return err
		}
		if last != before {
			break
		}
	}
	info, err := c.Info(ctx, "persistence").Result()
	if err == nil && containsLine(info, "rdb_last_bgsave_status:err") {
		return errors.New("redis couldn't save its data")
	}
	f, err := os.Open(filepath.Join(dataDir(dir), "dump.rdb")) //nolint:gosec // Wings' own folder
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func containsLine(s, line string) bool {
	for l := range strings.Lines(s) {
		if strings.TrimRight(l, "\r\n") == line {
			return true
		}
	}
	return false
}
