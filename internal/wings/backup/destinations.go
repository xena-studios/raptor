package backup

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Destination is where backups are stored: the built-in local one, or one
// the owner added (docs/WINGS.md#backup-destinations).
type Destination struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	engine.Config
	// UploadLimit caps upload speed in bytes per second (0: none).
	UploadLimit int64 `json:"upload_limit,omitempty"`
	// Status is how it's been doing; filled in listings, ignored on save.
	Status *DestinationStatus `json:"status,omitempty"`
}

// DestinationStatus is a destination's health.
type DestinationStatus struct {
	LastOKAt    time.Time `json:"last_ok_at,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
	// Size is what the repository stores, measured at most daily; -1 if
	// it hasn't been yet.
	Size   int64     `json:"size"`
	SizeAt time.Time `json:"size_at,omitzero"`
}

// redacted stands in for a secret in listings and events. Saving it back
// keeps the stored secret.
const redacted = "********"

// secrets are a destination's secret fields.
func (d *Destination) secrets() []*string {
	c := d.Config
	switch {
	case c.S3 != nil:
		return []*string{&c.S3.SecretKey}
	case c.B2 != nil:
		return []*string{&c.B2.Key}
	case c.Azure != nil:
		return []*string{&c.Azure.StorageKey, &c.Azure.SASToken}
	case c.SFTP != nil:
		return []*string{&c.SFTP.Password, &c.SFTP.PrivateKey}
	case c.WebDAV != nil:
		return []*string{&c.WebDAV.Password}
	case c.Rclone != nil:
		// The config holds the remote's tokens.
		return []*string{&c.Rclone.Config}
	}
	return nil
}

// clone copies d deeply enough to change its config.
func (d Destination) clone() Destination {
	b, _ := json.Marshal(d) //nolint:errchkjson // plain data
	var out Destination
	_ = json.Unmarshal(b, &out)
	return out
}

// Redacted returns the destination without its secrets, for events and
// listings.
func (d Destination) Redacted() Destination {
	d = d.clone()
	for _, s := range d.secrets() {
		if *s != "" {
			*s = redacted
		}
	}
	return d
}

// keepSecrets fills secrets left empty (or redacted) from the stored
// destination, if it's the same type.
func (d *Destination) keepSecrets(old Destination) {
	if d.Type != old.Type {
		return
	}
	olds := old.secrets()
	for i, s := range d.secrets() {
		if (*s == "" || *s == redacted) && i < len(olds) {
			*s = *olds[i]
		}
	}
}

// Paths a folder destination can't be in or above: the system's, and
// Raptor's own (servers' files, its database, the local backups).
var systemPaths = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/usr", "/var/lib", "/var/log", "/var/run"}

func (m *Manager) validate(d *Destination) error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" || utf8.RuneCountInString(d.Name) > maxNameLen {
		return fmt.Errorf("the name must be 1 to %d characters", maxNameLen)
	}
	if d.UploadLimit < 0 {
		return errors.New("the upload limit can't be negative")
	}
	if d.Type == engine.Local || !slices.Contains(engine.Types, d.Type) {
		return fmt.Errorf("%q isn't a destination type that can be added", d.Type)
	}
	set := 0
	c := d.Config
	for _, p := range []bool{c.Folder != nil, c.S3 != nil, c.B2 != nil, c.Azure != nil, c.SFTP != nil, c.WebDAV != nil, c.Rclone != nil} {
		if p {
			set++
		}
	}
	if set != 1 {
		return errors.New("a destination has the settings of exactly one type")
	}
	switch d.Type {
	case engine.Folder:
		if c.Folder == nil {
			break
		}
		return m.validateFolder(c.Folder)
	case engine.S3:
		if c.S3 == nil {
			break
		}
		s := c.S3
		if s.Endpoint == "" || s.Bucket == "" || s.AccessKey == "" || s.SecretKey == "" {
			return errors.New("an S3 destination needs an endpoint, a bucket, an access key, and a secret key")
		}
		if strings.Contains(s.Endpoint, "://") && !strings.HasPrefix(s.Endpoint, "https://") && !strings.HasPrefix(s.Endpoint, "http://") {
			return fmt.Errorf("the endpoint %q must be a host name or an http(s) URL", s.Endpoint)
		}
		return nil
	case engine.B2:
		if c.B2 == nil {
			break
		}
		if c.B2.Bucket == "" || c.B2.KeyID == "" || c.B2.Key == "" {
			return errors.New("a B2 destination needs a bucket, a key ID, and an application key")
		}
		return nil
	case engine.Azure:
		if c.Azure == nil {
			break
		}
		a := c.Azure
		if a.Container == "" || a.StorageAccount == "" || (a.StorageKey == "") == (a.SASToken == "") {
			return errors.New("an Azure destination needs a container, a storage account, and either its key or a SAS token")
		}
		return nil
	case engine.SFTP:
		if c.SFTP == nil {
			break
		}
		return validateSFTP(c.SFTP)
	case engine.WebDAV:
		if c.WebDAV == nil {
			break
		}
		u, err := url.Parse(c.WebDAV.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("a WebDAV destination needs an http(s) URL")
		}
		return nil
	case engine.Rclone:
		if c.Rclone == nil {
			break
		}
		return validateRclone(c.Rclone)
	}
	return fmt.Errorf("a %s destination needs its %s settings", d.Type, d.Type)
}

// validateFolder keeps a folder destination out of the system's and
// Raptor's own directories: backups written as root there could break the
// machine, or land where players can read them.
func (m *Manager) validateFolder(f *engine.FolderConfig) error {
	p := filepath.Clean(f.Path)
	if !filepath.IsAbs(f.Path) || p == "/" {
		return errors.New("the folder must be an absolute path, such as /mnt/backups")
	}
	f.Path = p
	// A link could point anywhere: both the path and where it leads count.
	paths := []string{p}
	if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
		paths = append(paths, real)
	}
	inside := func(p, dir string) bool {
		dir = filepath.Clean(dir)
		return p == dir || strings.HasPrefix(p, dir+"/") || strings.HasPrefix(dir, p+"/")
	}
	for _, dir := range append(slices.Clone(systemPaths), m.o.ReservedPaths...) {
		for _, p := range paths {
			if dir != "" && inside(p, dir) {
				return fmt.Errorf("the folder can't be in or contain %s", dir)
			}
		}
	}
	return nil
}

func validateSFTP(s *engine.SFTPConfig) error {
	if s.Host == "" || s.Username == "" || s.Path == "" {
		return errors.New("an SFTP destination needs a host, a username, and a path")
	}
	if s.Port < 0 || s.Port > 65535 {
		return errors.New("the port must be between 1 and 65535")
	}
	if s.HostKey == "" {
		return errors.New("an SFTP destination needs the server's host key; test the connection to get it")
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s.HostKey)); err != nil {
		return errors.New("the host key isn't an SSH public key")
	}
	ways := 0
	if s.Password != "" {
		ways++
	}
	if s.UseNodeKey {
		ways++
	}
	if s.PrivateKey != "" {
		ways++
		if _, err := ssh.ParseRawPrivateKey([]byte(s.PrivateKey)); err != nil {
			return errors.New("the private key isn't a PEM private key without a passphrase")
		}
	}
	if ways != 1 {
		return errors.New("an SFTP destination signs in with one of: a password, this node's key, or a private key")
	}
	return nil
}

// rcloneTypes are the rclone backends a destination can use: storage
// services, nothing that runs a command or reaches the node's own disk.
var rcloneTypes = []string{
	"azureblob", "b2", "box", "drive", "dropbox", "fichier", "filefabric", "gcs", "gofile", "hidrive",
	"iclouddrive", "internetarchive", "jottacloud", "koofr", "mailru", "mega", "onedrive", "opendrive",
	"oos", "pcloud", "pikpak", "premiumizeme", "protondrive", "putio", "qingstor", "s3", "seafile",
	"sharefile", "storj", "sugarsync", "swift", "uptobox", "yandex", "zoho",
}

// rcloneDenied are option names that can make rclone run a program.
var rcloneDenied = []string{"command", "ssh", "exec", "program", "helper", "cmd", "shell"}

// validateRclone allows one remote of a storage backend. rclone runs as
// root beside the backup worker, and some backends and options run
// programs (sftp's "ssh", local paths through "alias", ...), so anything
// else is refused.
func validateRclone(r *engine.RcloneConfig) error {
	name, path, ok := strings.Cut(r.Remote, ":")
	_ = path
	if !ok || name == "" {
		return errors.New(`the rclone remote is "name:path", with name a remote in the config`)
	}
	sections := map[string]map[string]string{}
	var cur string
	sc := bufio.NewScanner(strings.NewReader(r.Config))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			cur = strings.TrimSpace(line[1 : len(line)-1])
			if _, dup := sections[cur]; dup {
				return fmt.Errorf("the rclone config has the remote %q twice", cur)
			}
			sections[cur] = map[string]string{}
		default:
			k, v, ok := strings.Cut(line, "=")
			if !ok || cur == "" {
				return errors.New("the rclone config isn't in rclone's format")
			}
			sections[cur][strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	if len(sections) != 1 || sections[name] == nil {
		return fmt.Errorf("the rclone config must hold just the remote %q", name)
	}
	opts := sections[name]
	if !slices.Contains(rcloneTypes, opts["type"]) {
		return fmt.Errorf("rclone remotes of type %q can't be used: only storage services, not ones that reach this node or run programs", opts["type"])
	}
	for k := range opts {
		for _, bad := range rcloneDenied {
			if strings.Contains(k, bad) {
				return fmt.Errorf("the rclone option %q isn't allowed", k)
			}
		}
	}
	return nil
}

// Destinations lists the destinations, secrets redacted, with their status.
func (m *Manager) Destinations(ctx context.Context) ([]Destination, error) {
	rows, err := m.o.Store.Read.ListBackupDestinations(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Destination, 0, len(rows))
	for _, r := range rows {
		d, err := destFromRow(r)
		if err != nil {
			return nil, err
		}
		d = d.Redacted()
		d.Status = statusFromRow(r)
		out = append(out, d)
	}
	return out, nil
}

// Destination returns one destination, secrets redacted.
func (m *Manager) Destination(ctx context.Context, id string) (Destination, error) {
	r, err := m.o.Store.Read.GetBackupDestination(ctx, id)
	if err != nil {
		return Destination{}, ErrDestination
	}
	d, err := destFromRow(r)
	if err != nil {
		return Destination{}, err
	}
	d = d.Redacted()
	d.Status = statusFromRow(r)
	return d, nil
}

func statusFromRow(r store.BackupDestination) *DestinationStatus {
	s := &DestinationStatus{LastError: r.LastError, Size: -1}
	ms := func(v sql.NullInt64) time.Time {
		if !v.Valid {
			return time.Time{}
		}
		return time.UnixMilli(v.Int64)
	}
	s.LastOKAt, s.LastErrorAt, s.SizeAt = ms(r.LastOkAt), ms(r.LastErrorAt), ms(r.SizeAt)
	if r.Size.Valid {
		s.Size = r.Size.Int64
	}
	return s
}

// destFromRow reads a stored destination: config holds its type's settings.
func destFromRow(r store.BackupDestination) (Destination, error) {
	d := Destination{ID: r.ID, Name: r.Name, Type: r.Type, UploadLimit: r.UploadLimit}
	var target any
	switch r.Type {
	case engine.Local:
		return d, nil
	case engine.Folder:
		d.Folder = &engine.FolderConfig{}
		target = d.Folder
	case engine.S3:
		d.S3 = &engine.S3Config{}
		target = d.S3
	case engine.B2:
		d.B2 = &engine.B2Config{}
		target = d.B2
	case engine.Azure:
		d.Azure = &engine.AzureConfig{}
		target = d.Azure
	case engine.SFTP:
		d.SFTP = &engine.SFTPConfig{}
		target = d.SFTP
	case engine.WebDAV:
		d.WebDAV = &engine.WebDAVConfig{}
		target = d.WebDAV
	case engine.Rclone:
		d.Rclone = &engine.RcloneConfig{}
		target = d.Rclone
	default:
		return d, fmt.Errorf("destination %s has an unknown type %q", r.ID, r.Type)
	}
	if err := json.Unmarshal([]byte(r.Config), target); err != nil {
		return d, fmt.Errorf("destination %s: %w", r.ID, err)
	}
	return d, nil
}

// settings is the destination's type's settings, nil for local.
func (d Destination) settings() any {
	c := d.Config
	for _, v := range []any{c.Folder, c.S3, c.B2, c.Azure, c.SFTP, c.WebDAV, c.Rclone} {
		if !reflect.ValueOf(v).IsNil() {
			return v
		}
	}
	return nil
}

// config is what's stored of a destination's settings: its type's, with
// their credentials (the node's own database).
func (d Destination) config() ([]byte, error) {
	v := d.settings()
	if v == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(v)
}

// prepare validates a destination to save or test, filling secrets left
// out from the stored one with the same ID.
func (m *Manager) prepare(ctx context.Context, q *store.Queries, d *Destination) error {
	d.Status = nil
	if d.ID != "" {
		r, err := q.GetBackupDestination(ctx, d.ID)
		if err != nil {
			return ErrDestination
		}
		old, err := destFromRow(r)
		if err != nil {
			return err
		}
		if d.Type == "" {
			d.Type = old.Type
		}
		if d.Type != old.Type {
			return fmt.Errorf("%w: a destination's type can't change; add a new one", ErrInvalid)
		}
		d.keepSecrets(old)
	}
	if err := m.validate(d); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// SaveDestination adds a destination (d.ID empty) or replaces one. Secrets
// left empty or redacted keep the stored ones. It returns the destination's
// ID.
func (m *Manager) SaveDestination(ctx context.Context, d Destination) (string, error) {
	if d.ID == LocalDestination {
		return "", fmt.Errorf("%w: the local destination can't be changed", ErrInvalid)
	}
	now := m.o.Now().UnixMilli()
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := m.prepare(ctx, q, &d); err != nil {
			return err
		}
		cfg, err := d.config()
		if err != nil {
			return err
		}
		if d.ID == "" {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			d.ID = id.String()
			err = q.InsertBackupDestination(ctx, store.InsertBackupDestinationParams{
				ID: d.ID, Name: d.Name, Type: d.Type, Config: string(cfg), UploadLimit: d.UploadLimit, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				return err
			}
		} else if err := q.UpdateBackupDestination(ctx, store.UpdateBackupDestinationParams{
			Name: d.Name, Config: string(cfg), UploadLimit: d.UploadLimit, UpdatedAt: now, ID: d.ID,
		}); err != nil {
			return err
		}
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventDestination, Data: map[string]any{"destination": d.Redacted()}})
		return err
	})
	if err != nil {
		return "", err
	}
	m.o.Events.Wake()
	return d.ID, nil
}

// DeleteDestination removes a destination no server's settings use. Its
// backups are forgotten; the data where it was is left alone.
func (m *Manager) DeleteDestination(ctx context.Context, id string) error {
	if id == LocalDestination {
		return fmt.Errorf("%w: the local destination can't be deleted", ErrInvalid)
	}
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if _, err := q.GetBackupDestination(ctx, id); err != nil {
			return ErrDestination
		}
		n, err := q.CountDestinationTargets(ctx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrInUse
		}
		if err := q.DeleteBackupDestination(ctx, id); err != nil {
			return err
		}
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventDestination, Data: map[string]any{"destination_id": id, "deleted": true}})
		return err
	})
	if err == nil {
		m.o.Events.Wake()
	}
	return err
}

// TestDestination checks a destination works, before or after saving it:
// it writes a small file there, reads it back, and removes it. d.ID set
// tests the stored destination with d's changes (secrets left out are the
// stored ones). The error says what went wrong in words for people.
func (m *Manager) TestDestination(ctx context.Context, d Destination) error {
	if d.ID == LocalDestination {
		dest, err := m.engineDest(ctx, LocalDestination)
		if err != nil {
			return err
		}
		return m.test(ctx, dest)
	}
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error { return m.prepare(ctx, q, &d) })
	if err != nil {
		return err
	}
	dest, err := m.toEngine(ctx, d)
	if err != nil {
		return err
	}
	return m.test(ctx, dest)
}

func (m *Manager) test(ctx context.Context, dest engine.Destination) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	_, err := m.o.Runner.Run(ctx, request{Op: opTest, Dest: dest, StateDir: m.o.StateDir}, nil, discard{})
	if err != nil {
		m.log.Info("backup destination test failed", "destination", dest.ID, "type", dest.Type, "err", err)
		return Explain(err)
	}
	return nil
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// toEngine is what the worker needs to open a destination.
func (m *Manager) toEngine(ctx context.Context, d Destination) (engine.Destination, error) {
	ed := engine.Destination{ID: d.ID, Type: d.Type, Config: d.clone().Config, UploadLimit: d.UploadLimit}
	if d.Type == engine.Local {
		ed.Path = m.o.LocalPath
	}
	if ed.SFTP != nil && ed.SFTP.UseNodeKey {
		key, err := m.sshKey(ctx)
		if err != nil {
			return ed, err
		}
		ed.SFTP.PrivateKey = string(pem.EncodeToMemory(key))
	}
	return ed, nil
}

// engineDest returns what the worker needs to open a stored destination.
func (m *Manager) engineDest(ctx context.Context, id string) (engine.Destination, error) {
	r, err := m.o.Store.Read.GetBackupDestination(ctx, id)
	if err != nil {
		return engine.Destination{}, ErrDestination
	}
	d, err := destFromRow(r)
	if err != nil {
		return engine.Destination{}, err
	}
	return m.toEngine(ctx, d)
}

// recordOutcome notes a destination worked or failed. A failure, and the
// first success after one, are events, so the Panel shows them.
func (m *Manager) recordOutcome(ctx context.Context, destID string, opErr error) {
	ctx = context.WithoutCancel(ctx)
	now := sql.NullInt64{Int64: m.o.Now().UnixMilli(), Valid: true}
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		r, err := q.GetBackupDestination(ctx, destID)
		if err != nil {
			return nil //nolint:nilerr // deleted meanwhile
		}
		recovered := opErr == nil && r.LastError != "" && (!r.LastOkAt.Valid || r.LastOkAt.Int64 < r.LastErrorAt.Int64)
		if opErr == nil {
			err = q.DestinationWorked(ctx, store.DestinationWorkedParams{LastOkAt: now, ID: destID})
		} else {
			err = q.DestinationFailed(ctx, store.DestinationFailedParams{LastError: Explain(opErr).Error(), LastErrorAt: now, ID: destID})
		}
		if err != nil || (opErr == nil && !recovered) {
			return err
		}
		data := map[string]any{"destination_id": destID, "ok": opErr == nil}
		if opErr != nil {
			data["error"] = Explain(opErr).Error()
		}
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventDestinationStatus, Data: data})
		return err
	})
	if err != nil {
		m.log.Warn("recording a destination's status failed", "destination", destID, "err", err)
		return
	}
	m.o.Events.Wake()
}

// --- SSH ---

// sshKeyName holds the node's backup SSH key in kv: one per node, for SFTP
// destinations that sign in with it.
const sshKeyName = "backup.ssh_key"

func (m *Manager) sshKey(ctx context.Context) (*pem.Block, error) {
	var block *pem.Block
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if v, err := q.GetKV(ctx, sshKeyName); err == nil {
			block, _ = pem.Decode(v)
			if block == nil {
				return errors.New("the node's backup SSH key is unreadable")
			}
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		block, err = ssh.MarshalPrivateKey(priv, "raptor-backup")
		if err != nil {
			return err
		}
		return q.SetKV(ctx, store.SetKVParams{Key: sshKeyName, Value: pem.EncodeToMemory(block)})
	})
	return block, err
}

// SSHPublicKey is the node's backup SSH key, in authorized_keys form, to
// add on an SFTP server.
func (m *Manager) SSHPublicKey(ctx context.Context) (string, error) {
	block, err := m.sshKey(ctx)
	if err != nil {
		return "", err
	}
	key, err := ssh.ParseRawPrivateKey(pem.EncodeToMemory(block))
	if err != nil {
		return "", err
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " raptor-backup", nil
}

// HostKey is an SSH server's host key, to pin.
type HostKey struct {
	Key         string `json:"key"`         // authorized_keys form
	Fingerprint string `json:"fingerprint"` // SHA256:...
}

// errGotKey ends the handshake once the host key is seen.
var errGotKey = errors.New("got the host key")

// FetchHostKey connects to an SSH server just far enough to read its host
// key, for the owner to check and pin.
func FetchHostKey(ctx context.Context, host string, port int) (HostKey, error) {
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return HostKey{}, Explain(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var got ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: "raptor",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errGotKey
		},
	}
	_, _, _, err = ssh.NewClientConn(conn, addr, cfg)
	if got == nil {
		return HostKey{}, Explain(err)
	}
	return HostKey{
		Key:         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(got))),
		Fingerprint: ssh.FingerprintSHA256(got),
	}, nil
}

// Explained is a destination error in words for people. Error is the
// sentence; the storage library's own error is kept for logs (Unwrap).
type Explained struct {
	Msg string
	Err error
}

func (e *Explained) Error() string { return e.Msg }

func (e *Explained) Unwrap() error { return e.Err }

// Explain turns an error from a destination into a sentence for people, by
// what the storage libraries say. Ones it doesn't recognize get a plain
// sentence too; what the library said is in the node's log.
func Explain(err error) error {
	var ex *Explained
	if err == nil || errors.As(err, &ex) {
		return err
	}
	s := strings.ToLower(err.Error())
	has := func(subs ...string) bool {
		for _, x := range subs {
			if strings.Contains(s, x) {
				return true
			}
		}
		return false
	}
	msg := "the destination couldn't be used; the node's log (raptor logs) has the details"
	switch {
	case errors.Is(err, engine.ErrNoRclone):
		msg = engine.ErrNoRclone.Error()
	case has("knownhosts", "key mismatch", "host key"):
		msg = "the server's host key isn't the one saved; if it was changed on purpose, test again and save the new key"
	case has("unable to authenticate", "permission denied", "accessdenied", "access denied", "invalidaccesskeyid", "signaturedoesnotmatch", "unauthorized", "401", "403", "bad_auth_token", "authenticationfailed", "invalid credentials"):
		msg = "the credentials were refused: check the key or password, and that it may write there"
	case has("nosuchbucket", "bucket does not exist", "containernotfound", "no such bucket", "bucket_not_found"):
		msg = "the bucket or container doesn't exist"
	case has("no such host", "server misbehaving"):
		msg = "the host name doesn't resolve; check it's spelled right"
	case has("connection refused"):
		msg = "the server refused the connection; check the host and port"
	case has("i/o timeout", "deadline exceeded", "timed out", "no route to host", "network is unreachable"):
		msg = "the server didn't answer in time; check the address, and that a firewall lets this node reach it"
	case has("x509", "certificate"):
		msg = "the server's TLS certificate isn't trusted"
	case has("no space left", "quota", "insufficient storage", "507"):
		msg = "the destination is full"
	case has("read-only file system"):
		msg = "the destination is read-only"
	case has("no such file or directory") && !has("rclone"):
		msg = "the folder or path doesn't exist there"
	}
	return &Explained{Msg: msg, Err: err}
}
