package host

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Stats are the machine's resources, for the Panel's node health page. On
// systems without /proc (development on macOS) the /proc fields stay zero.
type Stats struct {
	CPUs            int     `json:"cpus"`
	Load1           float64 `json:"load1"`
	Load5           float64 `json:"load5"`
	Load15          float64 `json:"load15"`
	MemoryTotal     int64   `json:"memory_total"`     // bytes
	MemoryAvailable int64   `json:"memory_available"` // bytes
	UptimeSeconds   int64   `json:"uptime_seconds"`
}

// ReadStats reads /proc/loadavg, /proc/meminfo, and /proc/uptime.
func ReadStats() Stats {
	s := Stats{CPUs: runtime.NumCPU()}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 3 {
			s.Load1, _ = strconv.ParseFloat(f[0], 64)
			s.Load5, _ = strconv.ParseFloat(f[1], 64)
			s.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 {
				continue
			}
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			switch fields[0] {
			case "MemTotal:":
				s.MemoryTotal = kb << 10
			case "MemAvailable:":
				s.MemoryAvailable = kb << 10
			}
		}
		_ = f.Close()
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			up, _ := strconv.ParseFloat(f[0], 64)
			s.UptimeSeconds = int64(up)
		}
	}
	return s
}
