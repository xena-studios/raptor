package server

import (
	"path/filepath"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/storage"
)

// A server's files are on its disk: the main volume, or a data disk.
func TestServerDisks(t *testing.T) {
	main := "0199a000-0000-7000-8000-00000000aaaa"
	onDisk := "0199a000-0000-7000-8000-00000000bbbb"
	m := &Manager{
		o: Options{VolumesDir: "/v", DisksDir: "/state/disks", Storage: &storage.Volume{Path: "/v", Soft: true}},
		servers: map[string]*instance{
			main:   {id: main},
			onDisk: {id: onDisk, disk: "abcd1234"},
		},
	}
	if d, err := m.serverDir(main); err != nil || d != "/v/"+main {
		t.Errorf("main: %s, %v", d, err)
	}
	if d, err := m.serverDir(onDisk); err != nil || d != filepath.Join("/state/disks/abcd1234", onDisk) {
		t.Errorf("data disk: %s, %v", d, err)
	}
	if v, err := m.volumeFor("abcd1234"); err != nil || v.Path != "/state/disks/abcd1234" || !v.Soft {
		t.Errorf("volume: %+v, %v", v, err)
	}
	if v, _ := m.volumeFor(""); v != m.o.Storage {
		t.Error("main volume")
	}
	// A corrupt disk ID never reaches a path.
	m.servers[onDisk].disk = "../../etc"
	if _, err := m.serverDir(onDisk); err == nil {
		t.Error("a bad disk ID resolved")
	}
	// A volume with nothing mounted is refused before anything is written.
	m.o.Storage.Soft = false
	v, _ := m.volumeFor("abcd1234")
	if err := v.Check(); err == nil {
		t.Error("an unmounted data disk passed")
	}
}
