package support

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3 stores bundles in an S3-compatible bucket.
type S3 struct {
	client *minio.Client
	bucket string
}

// NewS3 connects to a bucket. The endpoint is a host name (HTTPS), or an
// http:// URL for local testing.
func NewS3(endpoint, region, bucket, accessKey, secretKey string) (*S3, error) {
	secure := !strings.HasPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	return &S3{client: c, bucket: bucket}, nil
}

// Put implements Store.
func (s *S3) Put(ctx context.Context, key string, body []byte, meta map[string]string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{
		ContentType:  "application/gzip",
		UserMetadata: meta,
	})
	return err
}

// Dir stores bundles in a local directory (development).
type Dir string

// Put implements Store. Keys are the service's own (a date and a code).
func (d Dir) Put(_ context.Context, key string, body []byte, _ map[string]string) error {
	p := filepath.Join(string(d), filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil { //nolint:gosec // see above
		return err
	}
	return os.WriteFile(p, body, 0o600) //nolint:gosec // see above
}
