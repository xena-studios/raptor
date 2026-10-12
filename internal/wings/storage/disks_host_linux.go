package storage

import (
	"os"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func hostMounts() map[string][]string {
	b, err := os.ReadFile("/proc/1/mountinfo")
	if err != nil {
		return nil
	}
	return HostMounts(b)
}

// HostMounts reads where each device is mounted in the host's namespace
// (PID 1's mountinfo): only mounts of a filesystem's root, so bind mounts of
// a directory on it don't count.
func HostMounts(mountinfo []byte) map[string][]string {
	out := map[string][]string{}
	for line := range strings.Lines(string(mountinfo)) {
		f := strings.Fields(line)
		if len(f) < 5 || f[3] != "/" {
			continue
		}
		mp := unescapeMountinfo(f[4])
		if !slices.Contains(out[f[2]], mp) {
			out[f[2]] = append(out[f[2]], mp)
		}
	}
	// Swap isn't in mountinfo.
	if b, err := os.ReadFile("/proc/swaps"); err == nil {
		for line := range strings.Lines(string(b)) {
			if dev, _, ok := strings.Cut(line, " "); ok && strings.HasPrefix(dev, "/dev/") {
				if mm := majMin(dev); mm != "" {
					out[mm] = append(out[mm], "[SWAP]")
				}
			}
		}
	}
	return out
}

func majMin(dev string) string {
	var st unix.Stat_t
	if err := unix.Stat(dev, &st); err != nil {
		return ""
	}
	return strings.Join([]string{itoa(unix.Major(st.Rdev)), itoa(unix.Minor(st.Rdev))}, ":")
}

func itoa(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
