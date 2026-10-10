package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/jobs"
)

const hostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

// A server backing up to two destinations: one job, a backup on each,
// and each destination's own retention.
func TestSeveralDestinations(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	v.write("world/level.dat", "v1")
	nas, err := v.m.SaveDestination(ctx, Destination{Name: "NAS", Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: filepath.Join(v.dir, "nas")}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.m.SetPolicy(ctx, srvID, Policy{Targets: []Target{
		{DestinationID: LocalDestination, Retention: Retention{KeepLast: 1}},
		{DestinationID: nas, Retention: Retention{KeepLast: 3}},
	}}); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		list, err := v.m.Create(ctx, srvID, CreateOptions{Kind: KindManual})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 2 || list[0].JobID != list[1].JobID || list[0].DestinationID != LocalDestination || list[1].DestinationID != nas {
			t.Fatalf("created %+v", list)
		}
		if j := v.wait(list[0].JobID); j.Status != jobs.Succeeded {
			log, _ := v.jobs.Log(j.ID)
			t.Fatalf("job %s: %s\n%s", j.Status, j.Error, log)
		}
		v.clock.add(1)
	}
	count := map[string]int{}
	for _, b := range v.list() {
		if b.Status != StatusOK {
			t.Fatalf("backup %+v", b)
		}
		count[b.DestinationID]++
	}
	if count[LocalDestination] != 1 || count[nas] != 3 {
		t.Fatalf("kept per destination: %v", count)
	}

	// One destination only, when asked; one the server doesn't use, refused.
	list, err := v.m.Create(ctx, srvID, CreateOptions{Kind: KindManual, DestinationID: nas})
	if err != nil || len(list) != 1 || list[0].DestinationID != nas {
		t.Fatalf("to one destination: %+v, %v", list, err)
	}
	v.wait(list[0].JobID)
	if _, err := v.m.Create(ctx, srvID, CreateOptions{Kind: KindManual, DestinationID: "elsewhere"}); !errors.Is(err, ErrNotTarget) {
		t.Fatalf("to another destination: %v", err)
	}

	// The destination's health: it worked.
	d, err := v.m.Destination(ctx, nas)
	if err != nil || d.Status.LastOKAt.IsZero() || d.Status.LastError != "" {
		t.Fatalf("status = %+v, %v", d.Status, err)
	}
}

// A destination failing doesn't stop the others; it's recorded, explained,
// and an event the Panel sees.
func TestDestinationFailing(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	v.write("f", "x")
	bad := filepath.Join(v.dir, "a-file")
	v.write("../../a-file", "not a folder")
	broken, err := v.m.SaveDestination(ctx, Destination{Name: "Broken", Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: bad}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.m.SetPolicy(ctx, srvID, Policy{Targets: []Target{
		{DestinationID: broken, Retention: DefaultRetention},
		{DestinationID: LocalDestination, Retention: DefaultRetention},
	}}); err != nil {
		t.Fatal(err)
	}
	list, err := v.m.Create(ctx, srvID, CreateOptions{Kind: KindManual})
	if err != nil {
		t.Fatal(err)
	}
	if j := v.wait(list[0].JobID); j.Status != jobs.Failed {
		t.Fatalf("job %s", j.Status)
	}
	for _, b := range list {
		got, _ := v.m.Get(ctx, srvID, b.ID)
		want := map[string]string{broken: StatusFailed, LocalDestination: StatusOK}[b.DestinationID]
		if got.Status != want {
			t.Fatalf("%s: %s, want %s (%s)", b.DestinationID, got.Status, want, got.Error)
		}
	}
	d, _ := v.m.Destination(ctx, broken)
	if d.Status.LastError == "" || d.Status.LastErrorAt.IsZero() {
		t.Fatalf("status = %+v", d.Status)
	}
	if !slices.Contains(v.eventTypes(), EventDestinationStatus) {
		t.Fatalf("no status event: %v", v.eventTypes())
	}
	// Testing it says so too.
	if err := v.m.TestDestination(ctx, Destination{ID: broken}); err == nil {
		t.Fatal("testing a broken destination passed")
	}
	if err := v.m.TestDestination(ctx, Destination{ID: LocalDestination}); err != nil {
		t.Fatalf("testing the local destination: %v", err)
	}
}

func TestDestinationValidation(t *testing.T) {
	v := newEnv(t)
	v.m.o.ReservedPaths = []string{"/srv/raptor"}
	ok := map[string]Destination{
		"folder": {Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: "/mnt/nas/raptor"}}},
		"s3":     {Type: engine.S3, Config: engine.Config{S3: &engine.S3Config{Endpoint: "s3.us-west-004.backblazeb2.com", Bucket: "b", AccessKey: "a", SecretKey: "s"}}},
		"b2":     {Type: engine.B2, Config: engine.Config{B2: &engine.B2Config{Bucket: "b", KeyID: "k", Key: "s"}}},
		"azure":  {Type: engine.Azure, Config: engine.Config{Azure: &engine.AzureConfig{Container: "c", StorageAccount: "a", SASToken: "t"}}},
		"sftp":   {Type: engine.SFTP, Config: engine.Config{SFTP: &engine.SFTPConfig{Host: "h", Username: "u", Path: "backups", HostKey: hostKey, UseNodeKey: true}}},
		"webdav": {Type: engine.WebDAV, Config: engine.Config{WebDAV: &engine.WebDAVConfig{URL: "https://cloud.example.com/remote.php/dav/files/me/raptor"}}},
		"rclone": {Type: engine.Rclone, Config: engine.Config{Rclone: &engine.RcloneConfig{Remote: "gd:raptor", Config: "[gd]\ntype = drive\ntoken = {}\n"}}},
	}
	for name, d := range ok {
		d.Name = name
		if err := v.m.validate(&d); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := map[string]Destination{
		"local":                    {Type: engine.Local},
		"unknown type":             {Type: "ftp"},
		"no settings":              {Type: engine.S3},
		"two settings":             {Type: engine.S3, Config: engine.Config{S3: ok["s3"].S3, B2: ok["b2"].B2}},
		"settings of another type": {Type: engine.S3, Config: engine.Config{B2: ok["b2"].B2}},
		"relative folder":          {Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: "backups"}}},
		"root":                     {Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: "/"}}},
		"etc":                      {Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: "/etc/raptor"}}},
		"raptor's data":            {Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: "/srv/raptor/volumes/x"}}},
		"above raptor":             {Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: "/srv"}}},
		"dot dot":                  {Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: "/mnt/../etc"}}},
		"azure two ways":           {Type: engine.Azure, Config: engine.Config{Azure: &engine.AzureConfig{Container: "c", StorageAccount: "a", SASToken: "t", StorageKey: "k"}}},
		"sftp no host key":         {Type: engine.SFTP, Config: engine.Config{SFTP: &engine.SFTPConfig{Host: "h", Username: "u", Path: "p", Password: "pw"}}},
		"sftp bad host key":        {Type: engine.SFTP, Config: engine.Config{SFTP: &engine.SFTPConfig{Host: "h", Username: "u", Path: "p", HostKey: "nope", Password: "pw"}}},
		"sftp two logins":          {Type: engine.SFTP, Config: engine.Config{SFTP: &engine.SFTPConfig{Host: "h", Username: "u", Path: "p", HostKey: hostKey, Password: "pw", UseNodeKey: true}}},
		"sftp bad key":             {Type: engine.SFTP, Config: engine.Config{SFTP: &engine.SFTPConfig{Host: "h", Username: "u", Path: "p", HostKey: hostKey, PrivateKey: "nope"}}},
		"webdav ftp":               {Type: engine.WebDAV, Config: engine.Config{WebDAV: &engine.WebDAVConfig{URL: "ftp://x"}}},
		"rclone local":             {Type: engine.Rclone, Config: engine.Config{Rclone: &engine.RcloneConfig{Remote: "l:/etc", Config: "[l]\ntype = local\n"}}},
		"rclone sftp ssh":          {Type: engine.Rclone, Config: engine.Config{Rclone: &engine.RcloneConfig{Remote: "s:x", Config: "[s]\ntype = sftp\nssh = sh -c 'id'\n"}}},
		"rclone command":           {Type: engine.Rclone, Config: engine.Config{Rclone: &engine.RcloneConfig{Remote: "d:x", Config: "[d]\ntype = drive\nauth_command = touch /tmp/x\n"}}},
		"rclone alias":             {Type: engine.Rclone, Config: engine.Config{Rclone: &engine.RcloneConfig{Remote: "a:x", Config: "[a]\ntype = alias\nremote = /etc\n"}}},
		"rclone two":               {Type: engine.Rclone, Config: engine.Config{Rclone: &engine.RcloneConfig{Remote: "d:x", Config: "[d]\ntype = drive\n[l]\ntype = local\n"}}},
		"rclone wrong name":        {Type: engine.Rclone, Config: engine.Config{Rclone: &engine.RcloneConfig{Remote: "x:y", Config: "[d]\ntype = drive\n"}}},
		"negative limit":           {Type: engine.B2, Config: engine.Config{B2: ok["b2"].B2}, UploadLimit: -1},
	}
	for name, d := range bad {
		d.Name = "x"
		if err := v.m.validate(&d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Every type's secrets are hidden in listings and events, and saving the
// listing back keeps them.
func TestDestinationSecrets(t *testing.T) {
	v := newEnv(t)
	ctx := context.Background()
	for _, d := range []Destination{
		{Name: "s3", Type: engine.S3, Config: engine.Config{S3: &engine.S3Config{Endpoint: "e", Bucket: "b", AccessKey: "a", SecretKey: "SECRET-1"}}},
		{Name: "b2", Type: engine.B2, Config: engine.Config{B2: &engine.B2Config{Bucket: "b", KeyID: "k", Key: "SECRET-2"}}},
		{Name: "azure", Type: engine.Azure, Config: engine.Config{Azure: &engine.AzureConfig{Container: "c", StorageAccount: "a", StorageKey: "SECRET-3"}}},
		{Name: "sftp", Type: engine.SFTP, Config: engine.Config{SFTP: &engine.SFTPConfig{Host: "h", Username: "u", Path: "p", HostKey: hostKey, Password: "SECRET-4"}}},
		{Name: "webdav", Type: engine.WebDAV, Config: engine.Config{WebDAV: &engine.WebDAVConfig{URL: "https://x", Password: "SECRET-5"}}},
		{Name: "rclone", Type: engine.Rclone, Config: engine.Config{Rclone: &engine.RcloneConfig{Remote: "d:x", Config: "[d]\ntype = drive\ntoken = SECRET-6\n"}}},
	} {
		id, err := v.m.SaveDestination(ctx, d)
		if err != nil {
			t.Fatalf("%s: %v", d.Name, err)
		}
		listed, err := v.m.Destination(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := json.Marshal(listed); strings.Contains(string(b), "SECRET") {
			t.Fatalf("%s listed with its secret: %s", d.Name, b)
		}
		listed.Name += " renamed"
		if _, err := v.m.SaveDestination(ctx, listed); err != nil {
			t.Fatalf("%s saved back: %v", d.Name, err)
		}
		r, _ := v.db.Read.GetBackupDestination(ctx, id)
		if !strings.Contains(r.Config, "SECRET") {
			t.Fatalf("%s lost its secret: %s", d.Name, r.Config)
		}
		// A type can't change: it would mix one type's secrets into another.
		other := listed
		other.Type = map[bool]string{true: engine.B2, false: engine.WebDAV}[d.Type == engine.WebDAV]
		if _, err := v.m.SaveDestination(ctx, other); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s changed type: %v", d.Name, err)
		}
	}
	all, _ := v.events.Since(ctx, 0, 1000)
	for _, e := range all {
		if b, _ := json.Marshal(e.Data); strings.Contains(string(b), "SECRET") {
			t.Fatalf("an event has a secret: %s", b)
		}
	}
}

func TestPolicyJSON(t *testing.T) {
	var p Policy
	if err := json.Unmarshal([]byte(`{"destination_id": "d1", "keep_last": 4, "ignore": ["*.log"]}`), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Targets) != 1 || p.Targets[0].DestinationID != "d1" || p.Targets[0].KeepLast != 4 || p.Ignore[0] != "*.log" {
		t.Fatalf("the old form: %+v", p)
	}
	cur := Policy{Targets: []Target{{DestinationID: "local", Retention: Retention{KeepLast: 3, KeepDaily: 7}}}}
	same := Policy{Targets: []Target{{DestinationID: "local", Retention: Retention{KeepLast: 3, KeepDaily: 7}}, {DestinationID: "d1", Retention: Retention{KeepLast: 1}}}}
	if same.KeepsLess(cur) || !slices.Equal(same.Added(cur), []string{"d1"}) {
		t.Fatalf("adding a destination: keeps less %v, added %v", same.KeepsLess(cur), same.Added(cur))
	}
	less := Policy{Targets: []Target{{DestinationID: "local", Retention: Retention{KeepLast: 3, KeepDaily: 2}}}}
	if !less.KeepsLess(cur) {
		t.Fatal("lower daily keep not noticed")
	}
	dropped := Policy{Targets: []Target{{DestinationID: "d1", Retention: Retention{KeepLast: 1}}}}
	if dropped.KeepsLess(cur) {
		t.Fatal("dropping a destination deletes nothing")
	}
	if p := (Policy{Targets: []Target{{DestinationID: "d1"}, {DestinationID: "local"}}}); p.Offsite().DestinationID != "d1" || p.Primary().DestinationID != "d1" {
		t.Fatal("offsite/primary")
	}
	if p := (Policy{Targets: []Target{{DestinationID: "local"}}}); p.Offsite().DestinationID != "local" {
		t.Fatal("offsite without one")
	}
}

func TestExplain(t *testing.T) {
	for raw, want := range map[string]string{
		"ssh: handshake failed: knownhosts: key mismatch":             "host key",
		"AccessDenied: Access Denied status code: 403":                "credentials were refused",
		"NoSuchBucket: The specified bucket does not exist":           "doesn't exist",
		"dial tcp: lookup nas.local: no such host":                    "doesn't resolve",
		"dial tcp 10.0.0.5:22: connect: connection refused":           "refused the connection",
		"context deadline exceeded":                                   "didn't answer in time",
		"panic in some library: index out of range [3] with length 2": "node's log",
	} {
		got := Explain(errors.New(raw))
		if !strings.Contains(got.Error(), want) || strings.Contains(got.Error(), raw) {
			t.Errorf("%q: %q", raw, got)
		}
		if !strings.Contains(fmt.Sprint(errors.Unwrap(got)), raw) {
			t.Errorf("%q: the original is lost", raw)
		}
	}
}
