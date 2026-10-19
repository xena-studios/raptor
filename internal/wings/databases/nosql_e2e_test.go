//go:build e2e

package databases

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/xena-studios/raptor/internal/wings/store"
)

func redisAs(addr, user, pass string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr + ":6379", Username: user, Password: pass, DialTimeout: 5 * time.Second, MaxRetries: -1, DisableIdentity: true})
}

func mongoAs(t *testing.T, addr, user, pass, db string) *mongo.Client {
	t.Helper()
	c, err := mongo.Connect(options.Client().ApplyURI("mongodb://" + addr + ":27017/").SetDirect(true).
		SetAuth(options.Credential{Username: user, Password: pass, AuthSource: db}).SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Disconnect(context.Background()) })
	return c
}

// TestRedis makes a Redis instance and a server's database on it: its
// user does what apps do and no administration, the instance takes no
// second server, the read-only user only reads, and a backup's snapshot
// restores; then a version change keeps the keys.
func TestRedis(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E redis")
	in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E Redis", Engine: Redis, Version: "8.2", Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 256}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
	in = waitRunning(t, e.svc, in.ID)
	addr := in.Addresses[e.net.ID]
	d, pw, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "cache")
	if err != nil {
		t.Fatal(err)
	}
	c := redisAs(addr, d.User, pw)
	defer func() { _ = c.Close() }()
	if err := c.Set(ctx, "player:alex", "42", 0).Err(); err != nil {
		t.Fatalf("SET as the server's user: %v", err)
	}
	if err := c.Do(ctx, "KEYS", "*").Err(); err != nil {
		t.Errorf("KEYS: %v", err)
	}
	for _, cmd := range [][]any{{"CONFIG", "GET", "dir"}, {"MODULE", "LIST"}, {"ACL", "LIST"}, {"DEBUG", "SLEEP", "0"}, {"SHUTDOWN"}} {
		if err := c.Do(ctx, cmd...).Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") && !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("%v as the server's user: %v", cmd, err)
		}
	}
	if err := redisAs(addr, "default", "").Ping(ctx).Err(); err == nil {
		t.Error("the default user logs in")
	}
	// One server's data per instance.
	other := "0199e2e0-0000-7000-8000-00000000c002"
	if err := e.db.Write.InsertServer(ctx, store.InsertServerParams{
		ID: other, Name: "o", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x", Variables: "{}",
		Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed", Networks: `["` + e.net.ID + `"]`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.CreateDatabase(ctx, other, in.ID, "cache"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a second server's database on a Redis instance: %v", err)
	}
	env, _ := e.svc.Env(ctx, e.server)
	if !slices.ContainsFunc(env, func(v string) bool { return strings.HasPrefix(v, "REDIS_URL=redis://"+d.User+":") }) ||
		slices.ContainsFunc(env, func(v string) bool { return strings.HasPrefix(v, "DB_HOST=") }) {
		t.Errorf("env %v", env)
	}

	// The read-only user reads and writes nothing.
	ro, rpw, err := e.svc.SetReadOnly(ctx, e.server, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	rc := redisAs(addr, ro, rpw)
	defer func() { _ = rc.Close() }()
	if v, err := rc.Get(ctx, "player:alex").Result(); err != nil || v != "42" {
		t.Errorf("GET as the read-only user: %q %v", v, err)
	}
	if err := rc.Set(ctx, "x", "1", 0).Err(); err == nil {
		t.Error("the read-only user wrote")
	}

	// A new password works and the old one doesn't.
	pw2, err := e.svc.ResetPassword(ctx, e.server, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisAs(addr, d.User, pw).Ping(ctx).Err(); err == nil {
		t.Error("the old password still works")
	}
	c2 := redisAs(addr, d.User, pw2)
	defer func() { _ = c2.Close() }()

	// A backup's snapshot, restored over changes made since.
	dir, names, warning, err := e.svc.Dump(ctx, e.server, io.Discard)
	if err != nil || warning != "" || !slices.Equal(names, []string{"cache"}) {
		t.Fatalf("dump: %v %q %v", names, warning, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := c2.Set(ctx, "player:alex", "0", 0).Err(); err != nil {
		t.Fatal(err)
	}
	snap, _ := os.ReadFile(filepath.Join(dir, "cache.rdb"))
	if err := e.svc.replace(ctx, mustDB(t, e, d.ID), bytes.NewReader(snap), io.Discard); err != nil {
		t.Fatalf("restoring the snapshot: %v", err)
	}
	waitRunning(t, e.svc, in.ID)
	c3 := redisAs(addr, d.User, pw2)
	defer func() { _ = c3.Close() }()
	if v, err := c3.Get(ctx, "player:alex").Result(); err != nil || v != "42" {
		t.Errorf("after the restore: %q %v", v, err)
	}

	// A version change keeps the keys and the users.
	jobID, err := e.svc.ChangeVersion(ctx, in.ID, "8.10")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t, jobID); j.Status != "succeeded" {
		t.Fatalf("version change: %s", j.Error)
	}
	c4 := redisAs(addr, d.User, pw2)
	defer func() { _ = c4.Close() }()
	if v, err := c4.Get(ctx, "player:alex").Result(); err != nil || v != "42" {
		t.Errorf("after the version change: %q %v", v, err)
	}

	// Deleting the database drops its user and the data.
	if err := e.svc.DeleteDatabase(ctx, e.server, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := redisAs(addr, d.User, pw2).Ping(ctx).Err(); err == nil {
		t.Error("a deleted database's user still logs in")
	}
	_, r, _ := e.svc.instance(ctx, e.db.Read, in.ID)
	root, _ := e.svc.sealer.open(r.RootPassword)
	if n, err := redisKeys(ctx, e.svc.dir(in.ID), root); err != nil || len(n) != 0 {
		t.Errorf("keys left after deleting: %v %v", n, err)
	}
}

func mustDB(t *testing.T, e e2e, id string) store.Database {
	t.Helper()
	r, err := e.svc.database(context.Background(), e.server, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestMongoDB makes a MongoDB instance shared by two servers: each user
// reaches its own database alone, the read-only user only reads, a dump
// exports and imports back and restores from a backup, and a version
// change raises the feature compatibility version.
func TestMongoDB(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E mongodb")
	in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E MongoDB", Engine: MongoDB, Version: "7.0", Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 768}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
	in = waitRunning(t, e.svc, in.ID)
	addr := in.Addresses[e.net.ID]
	d, pw, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "stats")
	if err != nil {
		t.Fatal(err)
	}
	other := "0199e2e0-0000-7000-8000-00000000c003"
	if err := e.db.Write.InsertServer(ctx, store.InsertServerParams{
		ID: other, Name: "o", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x", Variables: "{}",
		Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed", Networks: `["` + e.net.ID + `"]`,
	}); err != nil {
		t.Fatal(err)
	}
	d2, pw2, err := e.svc.CreateDatabase(ctx, other, in.ID, "stats")
	if err != nil {
		t.Fatalf("a second server's database: %v", err)
	}
	c := mongoAs(t, addr, d.User, pw, d.Name)
	players := c.Database(d.Name).Collection("players")
	if _, err := players.InsertMany(ctx, []any{bson.D{{Key: "name", Value: "alex"}}, bson.D{{Key: "name", Value: "sam"}}}); err != nil {
		t.Fatalf("insert as the server's user: %v", err)
	}
	if err := c.Database(d2.Name).Collection("x").FindOne(ctx, bson.D{}).Err(); err == nil || errors.Is(err, mongo.ErrNoDocuments) {
		t.Errorf("a user read another server's database: %v", err)
	}
	if err := c.Database("admin").RunCommand(ctx, bson.D{{Key: "listUsers", Value: 1}}).Err(); err == nil {
		t.Error("a user listed admin's users")
	}
	_ = pw2
	env, _ := e.svc.Env(ctx, e.server)
	if !slices.ContainsFunc(env, func(v string) bool { return strings.HasPrefix(v, "MONGODB_URI=mongodb://"+d.User+":") }) {
		t.Errorf("env %v", env)
	}

	ro, rpw, err := e.svc.SetReadOnly(ctx, e.server, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	rc := mongoAs(t, addr, ro, rpw, d.Name)
	if n, err := rc.Database(d.Name).Collection("players").CountDocuments(ctx, bson.D{}); err != nil || n != 2 {
		t.Errorf("count as the read-only user: %d %v", n, err)
	}
	if _, err := rc.Database(d.Name).Collection("players").InsertOne(ctx, bson.D{{Key: "x", Value: 1}}); err == nil {
		t.Error("the read-only user wrote")
	}

	// Export, then import back over a dropped collection.
	jobID, path, err := e.svc.Export(ctx, e.server, d.ID, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t, jobID); j.Status != "succeeded" {
		t.Fatalf("export: %s", j.Error)
	}
	if !strings.HasSuffix(path, ".archive.gz") {
		t.Errorf("export path %s", path)
	}
	raw, _ := os.ReadFile(filepath.Join(e.fdir, e.server, path))
	if gz, err := gzip.NewReader(bytes.NewReader(raw)); err != nil {
		t.Fatalf("the export isn't gzip: %v", err)
	} else if b, _ := io.ReadAll(gz); !bytes.Contains(b, []byte("alex")) {
		t.Error("the export has no players")
	}
	if err := players.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	jobID, err = e.svc.Import(ctx, e.server, d.ID, path)
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t, jobID); j.Status != "succeeded" {
		t.Fatalf("import: %s", j.Error)
	}
	if n, _ := players.CountDocuments(ctx, bson.D{}); n != 2 {
		t.Errorf("after the import, %d players", n)
	}

	// A backup's dump, restored over changes made since.
	dir, names, warning, err := e.svc.Dump(ctx, e.server, io.Discard)
	if err != nil || warning != "" || !slices.Equal(names, []string{"stats"}) {
		t.Fatalf("dump: %v %q %v", names, warning, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if _, err := players.DeleteMany(ctx, bson.D{}); err != nil {
		t.Fatal(err)
	}
	archive, _ := os.ReadFile(filepath.Join(dir, "stats.archive"))
	if err := e.svc.replace(ctx, mustDB(t, e, d.ID), bytes.NewReader(archive), io.Discard); err != nil {
		t.Fatalf("restoring the dump: %v", err)
	}
	if n, _ := players.CountDocuments(ctx, bson.D{}); n != 2 {
		t.Errorf("after the restore, %d players", n)
	}

	// Versions: one major at a time, and the data and users carry over.
	if _, err := e.svc.ChangeVersion(ctx, in.ID, "9.0"); !errors.Is(err, ErrInvalid) {
		t.Errorf("skipping a major: %v", err)
	}
	_, r, _ := e.svc.instance(ctx, e.db.Read, in.ID)
	root, _ := e.svc.sealer.open(r.RootPassword)
	admin, err := mongoConnect(ctx, e.svc.dir(in.ID), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Disconnect(ctx) }()
	jobID, err = e.svc.ChangeVersion(ctx, in.ID, "8.0")
	if v8, _ := lookup(MongoDB, "8.0"); unavailable(v8, hostKernel()) != "" {
		// This kernel can't run 8.0 (SERVER-121912): it's refused up
		// front, with why.
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "6.19") {
			t.Errorf("8.0 on Linux %s: %v", hostKernel(), err)
		}
		t.Logf("Linux %s: MongoDB 8.0 refused as it should be; the version change isn't tried", hostKernel())
		deleteMongo(ctx, t, e, d, pw, addr, admin)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if j := e.wait(t, jobID); j.Status != "succeeded" {
		t.Fatalf("version change: %s", j.Error)
	}
	c2 := mongoAs(t, addr, d.User, pw, d.Name)
	if n, err := c2.Database(d.Name).Collection("players").CountDocuments(ctx, bson.D{}); err != nil || n != 2 {
		t.Errorf("after the version change: %d %v", n, err)
	}
	var fcv struct {
		FCV struct {
			Version string `bson:"version"`
		} `bson:"featureCompatibilityVersion"`
	}
	if err := admin.Database("admin").RunCommand(ctx, bson.D{{Key: "getParameter", Value: 1}, {Key: "featureCompatibilityVersion", Value: 1}}).Decode(&fcv); err != nil || fcv.FCV.Version != "8.0" {
		t.Errorf("feature compatibility version %q %v", fcv.FCV.Version, err)
	}

	deleteMongo(ctx, t, e, d, pw, addr, admin)
}

// deleteMongo deletes a database and checks it and its users are gone.
func deleteMongo(ctx context.Context, t *testing.T, e e2e, d Database, pw, addr string, admin *mongo.Client) {
	t.Helper()
	// Deleting the database drops it and its users.
	if err := e.svc.DeleteDatabase(ctx, e.server, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := mongoAs(t, addr, d.User, pw, d.Name).Ping(ctx, nil); err == nil {
		t.Error("a deleted database's user still logs in")
	}
	dbs, _ := admin.ListDatabaseNames(ctx, bson.D{})
	if slices.Contains(dbs, d.Name) {
		t.Errorf("the database is still there: %v", dbs)
	}
}
