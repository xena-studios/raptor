package host

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// Sample is the machine's counters at one moment, for its resource history:
// rates come from the difference between two samples. On systems without
// /proc it's zero, and the history stays empty.
type Sample struct {
	Time time.Time
	// CPUBusy and CPUTotal are /proc/stat's jiffies across every core.
	CPUBusy, CPUTotal uint64
	MemoryTotal       int64
	MemoryUsed        int64 // total less available
	// RxBytes and TxBytes are the physical interfaces' counters.
	RxBytes, TxBytes uint64
	Load1            float64
}

// ReadSample reads /proc/stat, /proc/meminfo, /proc/loadavg, and
// /proc/net/dev.
func ReadSample() Sample {
	s := Sample{Time: time.Now()}
	s.CPUBusy, s.CPUTotal = cpuTimes()
	st := ReadStats()
	s.MemoryTotal, s.Load1 = st.MemoryTotal, st.Load1
	if st.MemoryTotal > 0 {
		s.MemoryUsed = st.MemoryTotal - st.MemoryAvailable
	}
	for _, n := range readNetDev() {
		if n.Physical {
			s.RxBytes += n.RxBytes
			s.TxBytes += n.TxBytes
		}
	}
	return s
}

// cpuTimes returns the first line of /proc/stat as busy and total jiffies.
// Idle and iowait are the idle ones; guest time is already in user.
func cpuTimes() (busy, total uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	for i, v := range fields[1:] {
		if i >= 8 { // guest and guest_nice are counted in user and nice
			break
		}
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i != 3 && i != 4 { // idle, iowait
			busy += n
		}
	}
	return busy, total
}

// CPUPercent is the machine's CPU use between two samples, 0 to 100.
func CPUPercent(prev, cur Sample) float64 {
	if cur.CPUTotal <= prev.CPUTotal || cur.CPUBusy < prev.CPUBusy {
		return 0
	}
	return min(100, float64(cur.CPUBusy-prev.CPUBusy)/float64(cur.CPUTotal-prev.CPUTotal)*100)
}

// Interface is a network interface's traffic since boot.
type Interface struct {
	Name     string `json:"name"`
	RxBytes  uint64 `json:"rx_bytes"`
	TxBytes  uint64 `json:"tx_bytes"`
	Physical bool   `json:"physical"` // a device, not a bridge, veth, or loopback
}

// readNetDev reads /proc/net/dev. An interface is physical if it has a
// device in /sys (a virtual machine's virtio NIC counts).
func readNetDev() []Interface {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []Interface
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // the two header lines
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		n := Interface{Name: strings.TrimSpace(name)}
		n.RxBytes, _ = strconv.ParseUint(fields[0], 10, 64)
		n.TxBytes, _ = strconv.ParseUint(fields[8], 10, 64)
		_, err := os.Stat("/sys/class/net/" + n.Name + "/device")
		n.Physical = err == nil
		out = append(out, n)
	}
	return out
}
