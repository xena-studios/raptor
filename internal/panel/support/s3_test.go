package support_test

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/xena-studios/raptor/internal/panel/support"
)

// TestS3 stores a bundle in a real bucket: PANEL_TEST_S3 is
// http://<key>:<secret>@<host:port>/<bucket> (task test starts MinIO).
func TestS3(t *testing.T) {
	raw := os.Getenv("PANEL_TEST_S3")
	if raw == "" {
		t.Skip("PANEL_TEST_S3 not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := u.User.Password()
	bucket := strings.Trim(u.Path, "/")
	ctx := context.Background()
	admin, err := minio.New(u.Host, &minio.Options{Creds: credentials.NewStaticV4(u.User.Username(), secret, "")})
	if err != nil {
		t.Fatal(err)
	}
	// MinIO may still be starting.
	var ok bool
	for range 50 {
		if ok, err = admin.BucketExists(ctx, bucket); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		if err := admin.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	s, err := support.NewS3("http://"+u.Host, "us-east-1", bucket, u.User.Username(), secret)
	if err != nil {
		t.Fatal(err)
	}
	key := "bundles/test/" + t.Name() + ".tar.gz"
	if err := s.Put(ctx, key, []byte{0x1f, 0x8b, 1, 2, 3}, map[string]string{"node": "n1", "ip": "203.0.113.7"}); err != nil {
		t.Fatal(err)
	}
	info, err := admin.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 5 || info.ContentType != "application/gzip" || info.UserMetadata["Node"] != "n1" || info.UserMetadata["Ip"] != "203.0.113.7" {
		t.Errorf("stored: %d %s %v", info.Size, info.ContentType, info.UserMetadata)
	}
}
