// Package host applies host-level settings Wings depends on.
package host

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Slice is the systemd slice every Raptor container runs in.
const Slice = "raptor.slice"

// Memory reserved for the OS, Docker, and Wings, outside the slice: 10% of
// RAM, at least 1 GiB and at most 4 GiB.
const (
	minReserve = 1 << 30
	maxReserve = 4 << 30
)

// Reserve returns how much memory to keep outside the slice on a box with
// total bytes of RAM. override > 0 replaces the default.
func Reserve(total, override int64) int64 {
	if override > 0 {
		return override
	}
	return min(max(total/10, minReserve), maxReserve)
}

// ApplySlice starts the slice (so its cgroup exists for firewall rules) and
// caps its memory at total RAM minus the reserve, so game servers together
// can never starve the host. The limit is set at runtime and reapplied by
// Wings on every start, so it follows RAM changes.
func ApplySlice(ctx context.Context, reserve int64) (limit int64, err error) {
	total, err := MemTotal("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	limit = total - Reserve(total, reserve)
	if limit <= 0 {
		return 0, fmt.Errorf("memory reserve %d exceeds total memory %d", Reserve(total, reserve), total)
	}
	for _, args := range [][]string{
		{"set-property", "--runtime", Slice, "MemoryMax=" + strconv.FormatInt(limit, 10)},
		{"start", Slice},
	} {
		if out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput(); err != nil { //nolint:gosec // fixed arguments
			return 0, fmt.Errorf("systemctl %s: %w: %s", args[0], err, bytes.TrimSpace(out))
		}
	}
	return limit, nil
}

// MemTotal reads total RAM in bytes from a meminfo file.
func MemTotal(path string) (int64, error) {
	f, err := os.Open(path) //nolint:gosec // fixed /proc path
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("meminfo: %w", err)
			}
			return kb * 1024, nil
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("meminfo: no MemTotal")
}

// OOMKills returns how many processes the kernel's OOM killer has killed in
// the slice and everything below it (memory.events is hierarchical). Docker
// doesn't reliably report OOM kills on cgroup v2 (State.OOMKilled stays
// false and no "oom" event is sent), so Wings reads the kernel's counter.
func OOMKills(slice string) (int64, error) {
	return oomKills(filepath.Join("/sys/fs/cgroup", slice, "memory.events"))
}

func oomKills(path string) (int64, error) {
	b, err := os.ReadFile(path) //nolint:gosec // fixed cgroup path
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "oom_kill "); ok {
			return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
	}
	return 0, errors.New("memory.events has no oom_kill")
}
