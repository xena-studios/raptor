package databases

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// MongoDB (docs/DATABASES.md#redis-and-mongodb): instances are shared like
// the SQL engines', each database with its own user, stored in that
// database (its authSource) with readWrite on it alone; the read-only user
// has read. Wings' user is the instance's root, made by the image's
// entrypoint, and reaches it through its socket.

func mongoSocket(dir string) string { return filepath.Join(runDir(dir), "mongodb-27017.sock") }

// mongoSocketURI is the socket as a connection string's host.
const mongoSocketURI = "%2Frun%2Fmongodb%2Fmongodb-27017.sock"

func mongoSpec(s *containers.ServiceSpec, in Instance, dir, root string, maxConn int) {
	mem := in.Limits.MemoryMiB
	s.Env = append(s.Env, "MONGO_INITDB_ROOT_USERNAME="+adminUser, "MONGO_INITDB_ROOT_PASSWORD="+root)
	s.Mounts = []containers.Mount{
		{Source: dataDir(dir), Target: "/data/db"},
		{Source: runDir(dir), Target: "/run/mongodb"},
	}
	// The image's other volume, which nothing here uses.
	s.Tmpfs = append(s.Tmpfs, "/data/configdb")
	// WiredTiger's cache: 40% of the memory, at least its 256 MB minimum.
	cache := max(float64(mem)*0.4/1024, 0.25)
	s.Cmd = []string{
		"mongod", "--bind_ip_all", "--unixSocketPrefix", "/run/mongodb",
		"--wiredTigerCacheSizeGB", strconv.FormatFloat(cache, 'f', 2, 64),
		"--maxConns", strconv.Itoa(maxConn),
	}
}

// mongoConnect connects as Wings through the socket.
func mongoConnect(ctx context.Context, dir, root string) (*mongo.Client, error) {
	return mongoConnectAs(ctx, dir, adminUser, root, "admin")
}

func mongoConnectAs(_ context.Context, dir, user, pass, authSource string) (*mongo.Client, error) {
	o := options.Client().
		ApplyURI("mongodb://" + url.PathEscape(mongoSocket(dir))).
		SetDirect(true).
		SetAuth(options.Credential{Username: user, Password: pass, AuthSource: authSource}).
		SetConnectTimeout(5 * time.Second).
		SetServerSelectionTimeout(5 * time.Second)
	return mongo.Connect(o)
}

func mongoRun(ctx context.Context, dir, root, database string, cmds ...bson.D) error {
	c, err := mongoConnect(ctx, dir, root)
	if err != nil {
		return err
	}
	defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
	for _, cmd := range cmds {
		if err := c.Database(database).RunCommand(ctx, cmd).Err(); err != nil {
			return fmt.Errorf("%s: %w", cmd[0].Key, err)
		}
	}
	return nil
}

// mongoUser makes a user in a database, or gives an existing one this
// password and these roles.
func mongoUser(ctx context.Context, dir, root, database, user, pass, role string) error {
	if err := checkNames(database, user); err != nil {
		return err
	}
	if err := checkPassword(pass); err != nil {
		return err
	}
	roles := bson.A{bson.D{{Key: "role", Value: role}, {Key: "db", Value: database}}}
	err := mongoRun(ctx, dir, root, database, bson.D{{Key: "createUser", Value: user}, {Key: "pwd", Value: pass}, {Key: "roles", Value: roles}})
	var ce mongo.CommandError
	if errors.As(err, &ce) && ce.Code == 51003 { // already exists
		err = mongoRun(ctx, dir, root, database, bson.D{{Key: "updateUser", Value: user}, {Key: "pwd", Value: pass}, {Key: "roles", Value: roles}})
	}
	return err
}

func mongoCreate(ctx context.Context, dir, root, name, user, pass string) error {
	return mongoUser(ctx, dir, root, name, user, pass, "readWrite")
}

// mongoDrop drops a database and every user stored in it.
func mongoDrop(ctx context.Context, dir, root, name string) error {
	if err := checkNames(name); err != nil {
		return err
	}
	return mongoRun(ctx, dir, root, name, bson.D{{Key: "dropAllUsersFromDatabase", Value: 1}}, bson.D{{Key: "dropDatabase", Value: 1}})
}

// mongoEmpty drops a database's collections, keeping its users.
func mongoEmpty(ctx context.Context, dir, root, name string) error {
	if err := checkNames(name); err != nil {
		return err
	}
	return mongoRun(ctx, dir, root, name, bson.D{{Key: "dropDatabase", Value: 1}})
}

func mongoPassword(ctx context.Context, dir, root, database, user, pass string) error {
	if err := checkPassword(pass); err != nil {
		return err
	}
	return mongoRun(ctx, dir, root, database, bson.D{{Key: "updateUser", Value: user}, {Key: "pwd", Value: pass}})
}

func mongoDropUser(ctx context.Context, dir, root, database, user string) error {
	err := mongoRun(ctx, dir, root, database, bson.D{{Key: "dropUser", Value: user}})
	var ce mongo.CommandError
	if errors.As(err, &ce) && ce.Code == 11 { // no such user
		return nil
	}
	return err
}

func mongoPing(ctx context.Context, dir, root string) error {
	return mongoRun(ctx, dir, root, "admin", bson.D{{Key: "ping", Value: 1}})
}

// mongoFCV raises the feature compatibility version to the version
// running, after a version change: until then, the new version keeps the
// old one's data format.
func mongoFCV(ctx context.Context, dir, root, version string) error {
	return mongoRun(ctx, dir, root, "admin", bson.D{
		{Key: "setFeatureCompatibilityVersion", Value: version}, {Key: "confirm", Value: true},
	})
}

// mongoCounts counts every collection's documents in some databases, for
// checking a version change.
func mongoCounts(ctx context.Context, dir, root string, databases []string) (map[string]int64, error) {
	c, err := mongoConnect(ctx, dir, root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Disconnect(context.WithoutCancel(ctx)) }()
	out := map[string]int64{}
	for _, name := range databases {
		db := c.Database(name)
		colls, err := db.ListCollectionNames(ctx, bson.D{{Key: "type", Value: "collection"}})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, coll := range colls {
			if strings.HasPrefix(coll, "system.") {
				continue
			}
			n, err := db.Collection(coll).CountDocuments(ctx, bson.D{})
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", name, coll, err)
			}
			out[name+": "+coll] = n
		}
		out[name+":"] = int64(len(colls))
	}
	return out, nil
}

// mongoTool runs mongodump or mongorestore in the instance's container as
// a database's user. The password goes in a config file on the
// container's /tmp (a tmpfs), written through stdin and removed after,
// so it's never on a command line.
func (s *Service) mongoTool(ctx context.Context, instanceID, user, pass, database string, args []string, stdin io.Reader, stdout, log io.Writer) error {
	if err := checkPassword(pass); err != nil {
		return err
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	cfg := "/tmp/raptor-" + hex.EncodeToString(b) + ".yaml"
	name := ContainerName(instanceID)
	if _, err := s.o.Runtime.Exec(ctx, name, containers.ExecSpec{
		Cmd: []string{"sh", "-c", `umask 077 && cat > "$0"`, cfg}, User: "999:999",
		Stdin: strings.NewReader("password: " + pass + "\n"), Stdout: io.Discard, Stderr: log,
	}); err != nil {
		return err
	}
	defer func() {
		_, _ = s.o.Runtime.Exec(context.WithoutCancel(ctx), name, containers.ExecSpec{
			Cmd: []string{"rm", "-f", cfg}, User: "999:999", Stdout: io.Discard, Stderr: io.Discard,
		})
	}()
	uri := "mongodb://" + user + "@" + mongoSocketURI + "/?authSource=" + database
	cmd := append([]string{args[0], "--config=" + cfg, "--uri=" + uri, "--quiet"}, args[1:]...)
	var stderr tailBuffer
	if stdout == nil {
		stdout = io.Discard
	}
	code, err := s.o.Runtime.Exec(ctx, name, containers.ExecSpec{
		Cmd: cmd, User: "999:999", Stdin: stdin, Stdout: stdout, Stderr: io.MultiWriter(&stderr, log),
	})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s failed: %s", args[0], lastLine(stderr.String()))
	}
	return nil
}
