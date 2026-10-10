package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/azure"
	"github.com/kopia/kopia/repo/blob/filesystem"
	"github.com/kopia/kopia/repo/blob/s3"
	"github.com/kopia/kopia/repo/blob/sftp"
	"github.com/kopia/kopia/repo/blob/throttling"
	"github.com/kopia/kopia/repo/blob/webdav"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Destination types: one per way of reaching storage, from what Kopia has
// (docs/WINGS.md#backups). Services that speak S3 (Backblaze B2, R2,
// Wasabi, Google Cloud Storage's S3 API, ...) are S3 destinations, not
// types of their own. Kopia's experimental backends (rclone, Google Drive)
// and Google's own APIs (their SDK would add 18 MB to Wings) aren't built in.
const (
	Local  = "local"  // the node's own backup directory (paths.backups)
	Folder = "folder" // another directory on the node: a mounted NAS, a second disk
	S3     = "s3"     // S3-compatible: AWS, Backblaze B2, R2, Wasabi, MinIO, ...
	Azure  = "azure"  // Azure Blob Storage
	SFTP   = "sftp"
	WebDAV = "webdav"
	// Raptor is Raptor Backup Storage: Raptor's B2 bucket, through its S3
	// API, with a key the Panel made for this node's folder.
	Raptor = "raptor"
)

// Types lists the destination types.
var Types = []string{Local, Folder, S3, Azure, SFTP, WebDAV, Raptor}

// Destination is where a repository lives. One repository per destination
// holds every server's backups on the node, so identical files are stored
// once. Exactly one of the configs is set, for Type.
type Destination struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Path is the directory of the local destination.
	Path string `json:"path,omitempty"`
	Config
	// UploadLimit caps upload speed in bytes per second (0: none), so
	// backups leave room for players.
	UploadLimit int64 `json:"upload_limit,omitempty"`
}

// Config is a destination's settings, by type.
type Config struct {
	Folder *FolderConfig `json:"folder,omitempty"`
	S3     *S3Config     `json:"s3,omitempty"`
	Azure  *AzureConfig  `json:"azure,omitempty"`
	SFTP   *SFTPConfig   `json:"sftp,omitempty"`
	WebDAV *WebDAVConfig `json:"webdav,omitempty"`
	Raptor *S3Config     `json:"raptor,omitempty"`
}

// FolderConfig is a directory on the node.
type FolderConfig struct {
	Path string `json:"path"`
}

// S3Config is an S3-compatible bucket.
type S3Config struct {
	// Endpoint is a host[:port], or a URL: https:// (the default) or
	// http:// for a bucket on a trusted network.
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"` //nolint:gosec // it's the field, not a value
}

// AzureConfig is an Azure Blob Storage container. Either the account key or
// a SAS token.
type AzureConfig struct {
	Container      string `json:"container"`
	Prefix         string `json:"prefix,omitempty"`
	StorageAccount string `json:"storage_account"`
	StorageKey     string `json:"storage_key,omitempty"`
	SASToken       string `json:"sas_token,omitempty"`
	// StorageDomain is for clouds other than Azure's public one
	// (default blob.core.windows.net).
	StorageDomain string `json:"storage_domain,omitempty"`
}

// SFTPConfig is a directory on a server reached over SSH. The host key is
// pinned: a server that answers with another one is refused.
type SFTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"` // default 22
	Username string `json:"username"`
	Path     string `json:"path"`
	// HostKey is the server's public key, in authorized_keys form
	// ("ssh-ed25519 AAAA...").
	HostKey string `json:"host_key"`
	// Password, or else PrivateKey (PEM). Wings fills PrivateKey with the
	// node's own backup key when UseNodeKey is set.
	Password   string `json:"password,omitempty"`
	UseNodeKey bool   `json:"use_node_key,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
}

// WebDAVConfig is a WebDAV folder (Nextcloud, ownCloud, a NAS).
type WebDAVConfig struct {
	URL      string `json:"url"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// Addr is an SFTP server's host:port.
func (c *SFTPConfig) Addr() string {
	port := c.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

func (e *Engine) storage(ctx context.Context) (blob.Storage, error) {
	d := e.Dest
	limits := throttling.Limits{UploadBytesPerSecond: float64(d.UploadLimit)}
	switch {
	case d.Type == Local:
		return folder(ctx, d.Path, limits)
	case d.Type == Folder && d.Folder != nil:
		return folder(ctx, d.Folder.Path, limits)
	case d.Type == S3 && d.S3 != nil:
		c := d.S3
		endpoint, plain := strings.CutPrefix(c.Endpoint, "http://")
		endpoint = strings.TrimPrefix(endpoint, "https://")
		return s3.New(ctx, &s3.Options{
			BucketName: c.Bucket, Prefix: c.Prefix, Endpoint: strings.TrimSuffix(endpoint, "/"),
			DoNotUseTLS: plain, Region: c.Region, AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey,
			Limits: limits,
		}, false)
	case d.Type == Azure && d.Azure != nil:
		c := d.Azure
		return azure.New(ctx, &azure.Options{
			Container: c.Container, Prefix: c.Prefix, StorageAccount: c.StorageAccount, StorageKey: c.StorageKey,
			SASToken: c.SASToken, StorageDomain: c.StorageDomain, Limits: limits,
		}, false)
	case d.Type == SFTP && d.SFTP != nil:
		c := d.SFTP
		port := c.Port
		if port == 0 {
			port = 22
		}
		o := &sftp.Options{
			Path: c.Path, Host: c.Host, Port: port, Username: c.Username,
			// Only the pinned key: never ~/.ssh/known_hosts, never "accept new".
			KnownHostsData: knownhosts.Normalize(c.Addr()) + " " + strings.TrimSpace(c.HostKey),
			Limits:         limits,
		}
		if c.Password != "" {
			o.Password = c.Password
		} else {
			o.KeyData = c.PrivateKey
		}
		return sftp.New(ctx, o, true)
	case d.Type == Raptor && d.Raptor != nil:
		c := d.Raptor
		return s3.New(ctx, &s3.Options{
			BucketName: c.Bucket, Prefix: c.Prefix, Endpoint: c.Endpoint, Region: c.Region,
			AccessKeyID: c.AccessKey, SecretAccessKey: c.SecretKey, Limits: limits,
		}, false)
	case d.Type == WebDAV && d.WebDAV != nil:
		c := d.WebDAV
		return webdav.New(ctx, &webdav.Options{URL: c.URL, Username: c.Username, Password: c.Password, AtomicWrites: true, Limits: limits}, false)
	}
	return nil, fmt.Errorf("destination %s: unknown type %q, or its settings are missing", d.ID, d.Type)
}

func folder(ctx context.Context, path string, limits throttling.Limits) (blob.Storage, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("destination path %q isn't absolute", path)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, err
	}
	return filesystem.New(ctx, &filesystem.Options{Path: path, FileMode: 0o600, DirectoryMode: 0o700, Limits: limits}, true)
}

// testBlob is the prefix of the file a destination test writes and removes.
const testBlob = "raptor-test-"

// Test checks a destination works: it writes a small file, reads it back,
// and removes it. It doesn't need (or create) a repository.
func (e *Engine) Test(ctx context.Context) error {
	st, err := e.storage(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(ctx) }()
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := blob.ID(testBlob + hex.EncodeToString(b))
	want := []byte("Raptor checks it can store backups here. This file is removed straight away.\n")
	if err := st.PutBlob(ctx, id, sliceBytes(want), blob.PutOptions{}); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	defer func() { _ = st.DeleteBlob(context.WithoutCancel(ctx), id) }()
	var got bytesOutput
	if err := st.GetBlob(ctx, id, 0, -1, &got); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		return errors.New("read: what came back isn't what was written")
	}
	if err := st.DeleteBlob(ctx, id); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Size adds up what the destination's repository stores.
func (e *Engine) Size(ctx context.Context) (int64, error) {
	st, err := e.storage(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = st.Close(ctx) }()
	var n int64
	err = st.ListBlobs(ctx, "", func(m blob.Metadata) error {
		n += m.Length
		return nil
	})
	return n, err
}

// sliceBytes is a byte slice as blob.Bytes.
type sliceBytes []byte

func (b sliceBytes) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(b)
	return int64(n), err
}

func (b sliceBytes) Length() int { return len(b) }

func (b sliceBytes) Reader() io.ReadSeekCloser { return nopCloser{bytes.NewReader(b)} }

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

// bytesOutput is a blob.OutputBuffer.
type bytesOutput struct{ bytes.Buffer }

func (b *bytesOutput) Length() int { return b.Len() }
