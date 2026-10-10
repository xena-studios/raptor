//go:build e2e

package backup

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/jobs"
)

// versionedMinIO starts MinIO with a versioned bucket, as Raptor Backup
// Storage's B2 bucket is.
func versionedMinIO(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	name := fmt.Sprintf("raptor-e2e-minio-pit-%d", port)
	if out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:9000", port),
		"-e", "MINIO_ROOT_USER=raptor", "-e", "MINIO_ROOT_PASSWORD=raptor-secret",
		"cgr.dev/chainguard/minio", "server", "/tmp/data").CombinedOutput(); err != nil {
		t.Fatalf("start minio: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	endpoint := fmt.Sprintf("127.0.0.1:%d", port)
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4("raptor", "raptor-secret", "")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(500 * time.Millisecond) {
		if err = c.MakeBucket(ctx, "raptor", minio.MakeBucketOptions{}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("create bucket: %v", err)
		}
	}
	if err := c.EnableVersioning(ctx, "raptor"); err != nil {
		t.Fatalf("versioning: %v", err)
	}
	return endpoint
}

// A backup deleted from versioned storage (by retention, or by someone on a
// hacked node) is found again by looking at the storage as it was before.
func TestPointInTimeRecovery(t *testing.T) {
	ctx := context.Background()
	endpoint := versionedMinIO(t)
	v := newEnv(t)
	v.m.o.Now = time.Now // the storage's versions are in real time
	s3, err := v.m.SaveDestination(ctx, Destination{Name: "Versioned", Type: engine.S3, Config: engine.Config{S3: &engine.S3Config{
		Endpoint: "http://" + endpoint, Bucket: "raptor", Prefix: "node/", AccessKey: "raptor", SecretKey: "raptor-secret",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.m.SetPolicy(ctx, srvID, Policy{Targets: []Target{{DestinationID: s3, Retention: DefaultRetention}}}); err != nil {
		t.Fatal(err)
	}
	v.write("world/level.dat", "before the attack")
	b := v.backup()
	time.Sleep(2 * time.Second)
	before := time.Now()
	time.Sleep(2 * time.Second)

	// "The attack": the backup deleted, and Kopia's maintenance run.
	job, err := v.m.Delete(ctx, srvID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j := v.wait(job); j.Status != jobs.Succeeded {
		t.Fatalf("delete: %s %s", j.Status, j.Error)
	}
	if _, err := v.m.run(ctx, s3, request{Op: opMaintain}, nil, discard{}); err != nil {
		t.Fatal(err)
	}
	v.write("world/level.dat", "after the attack")

	dest, job, err := v.m.Recover(ctx, RecoverParams{FromDestinationID: s3, PointInTime: &before}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if j := v.wait(job); j.Status != jobs.Succeeded {
		log, _ := v.jobs.Log(j.ID)
		t.Fatalf("recover: %s %s\n%s", j.Status, j.Error, log)
	}
	var found *Backup
	for _, x := range v.list() {
		if x.DestinationID == dest && x.Kind == KindRecovered {
			found = x
		}
	}
	if found == nil {
		t.Fatal("the deleted backup wasn't found at the earlier time")
	}
	restore, err := v.m.Restore(ctx, srvID, found.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if j := v.wait(restore); j.Status != jobs.Succeeded {
		log, _ := v.jobs.Log(j.ID)
		t.Fatalf("restore: %s %s\n%s", j.Status, j.Error, log)
	}
	if got := v.read("world/level.dat"); got != "before the attack" {
		t.Fatalf("restored %q", got)
	}
}
