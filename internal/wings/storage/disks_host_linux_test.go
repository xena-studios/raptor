package storage

import (
	"slices"
	"testing"
)

func TestHostMounts(t *testing.T) {
	info := []byte(`22 1 254:1 / / rw,relatime shared:1 - ext4 /dev/vda1 rw
30 22 254:15 / /boot/efi rw shared:2 - vfat /dev/vda15 rw
40 22 254:1 /var/lib/docker/volumes/x /mnt/bind rw - ext4 /dev/vda1 rw
41 22 254:16 / /var/lib/raptor/disks/abc rw - xfs /dev/vdb rw,prjquota
42 22 254:16 / /var/lib/raptor/disks/abc rw - xfs /dev/vdb rw,prjquota
43 22 254:32 / /mnt/with\040space ro - iso9660 /dev/vdc ro
`)
	m := HostMounts(info)
	if !slices.Equal(m["254:1"], []string{"/"}) || !slices.Equal(m["254:16"], []string{"/var/lib/raptor/disks/abc"}) || m["254:32"][0] != "/mnt/with space" {
		t.Errorf("mounts: %v", m)
	}
}
