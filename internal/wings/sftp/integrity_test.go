package sftp

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Packet buffers are reused (sftp.WithRSAllocator): many reads and writes
// in flight at once, over several files on one session and on several
// sessions, must come back byte for byte. Run with -race.
func TestTransferIntegrity(t *testing.T) {
	e := newEnv(t)
	c, err := ssh.Dial("tcp", e.addr, &ssh.ClientConfig{
		User: "alice." + srvID, Auth: []ssh.AuthMethod{ssh.Password("hunter2")},
		HostKeyCallback: ssh.FixedHostKey(e.hostKey.PublicKey()),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var wg sync.WaitGroup
	for s := range 3 {
		cl, err := sftp.NewClient(c, sftp.UseConcurrentWrites(true), sftp.UseConcurrentReads(true), sftp.MaxConcurrentRequestsPerFile(64))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cl.Close() }()
		for i := range 4 {
			wg.Go(func() {
				data := make([]byte, 3<<20+i*4099+s)
				_, _ = rand.Read(data)
				name := fmt.Sprintf("f-%d-%d", s, i)
				w, err := cl.Create(name)
				if err != nil {
					t.Error(err)
					return
				}
				if _, err := w.ReadFrom(bytes.NewReader(data)); err != nil {
					t.Error(err)
				}
				if err := w.Close(); err != nil {
					t.Error(err)
				}
				if got, _ := os.ReadFile(filepath.Join(e.dir, name)); !bytes.Equal(got, data) { //nolint:gosec // test file
					t.Errorf("%s: uploaded %d bytes differ", name, len(got))
				}
				r, err := cl.Open(name)
				if err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = r.Close() }()
				var back bytes.Buffer
				if _, err := r.WriteTo(&back); err != nil {
					t.Error(err)
				}
				if !bytes.Equal(back.Bytes(), data) {
					t.Errorf("%s: downloaded %d bytes differ", name, back.Len())
				}
				// Small reads at odd offsets, all at once.
				var rg sync.WaitGroup
				for off := 0; off < len(data); off += len(data) / 37 {
					rg.Go(func() {
						buf := make([]byte, 1000)
						n, err := r.ReadAt(buf, int64(off))
						if err != nil && !errors.Is(err, io.EOF) {
							t.Error(err)
						}
						if !bytes.Equal(buf[:n], data[off:min(off+1000, len(data))]) {
							t.Errorf("%s: read at %d differs", name, off)
						}
					})
				}
				rg.Wait()
			})
		}
	}
	wg.Wait()
}
