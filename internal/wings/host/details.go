package host

import (
	"bufio"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Details are what the machine is, for the node's Health tab: its system,
// processor, memory in detail, and network interfaces. Fields /proc doesn't
// have stay empty.
type Details struct {
	OS       string `json:"os"`     // PRETTY_NAME from os-release
	Kernel   string `json:"kernel"` // the kernel release
	CPUModel string `json:"cpu_model"`
	// Memory, in bytes: cache is what the kernel can give back (page cache,
	// buffers, reclaimable slab).
	MemoryCache int64            `json:"memory_cache"`
	SwapTotal   int64            `json:"swap_total"`
	SwapFree    int64            `json:"swap_free"`
	Interfaces  []InterfaceState `json:"interfaces"`
}

// InterfaceState is an interface with its addresses and traffic.
type InterfaceState struct {
	Interface
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"` // CIDR form
}

// ReadDetails reads os-release, the kernel release, /proc/cpuinfo,
// /proc/meminfo, and the interfaces. Container interfaces (veth) are left
// out: there's one per running server, and the servers' own pages show
// their traffic.
func ReadDetails() Details {
	d := Details{OS: osName(), CPUModel: cpuModel()}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		d.Kernel = strings.TrimSpace(string(b))
	}
	mem := meminfo()
	d.MemoryCache = mem["Cached"] + mem["Buffers"] + mem["SReclaimable"]
	d.SwapTotal, d.SwapFree = mem["SwapTotal"], mem["SwapFree"]

	counters := map[string]Interface{}
	for _, n := range readNetDev() {
		counters[n.Name] = n
	}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if strings.HasPrefix(i.Name, "veth") {
			continue
		}
		st := InterfaceState{Interface: counters[i.Name], Up: i.Flags&net.FlagUp != 0}
		st.Name = i.Name
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			st.Addresses = append(st.Addresses, a.String())
		}
		d.Interfaces = append(d.Interfaces, st)
	}
	// Physical ones first, then by name.
	slices.SortStableFunc(d.Interfaces, func(a, b InterfaceState) int {
		if a.Physical != b.Physical {
			if a.Physical {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return d
}

func osName() string {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		b, err := os.ReadFile(p) //nolint:gosec // a fixed list
		if err != nil {
			continue
		}
		for line := range strings.Lines(string(b)) {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PRETTY_NAME="); ok {
				if u, err := strconv.Unquote(v); err == nil {
					return u
				}
				return strings.Trim(v, `'"`)
			}
		}
	}
	return ""
}

func cpuModel() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		// x86 says "model name"; arm64 has no name, only its parts.
		switch strings.TrimSpace(k) {
		case "model name", "Model", "Hardware":
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// meminfo returns /proc/meminfo in bytes.
func meminfo() map[string]int64 {
	out := map[string]int64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(fields[1], 10, 64)
		out[strings.TrimSuffix(fields[0], ":")] = kb << 10
	}
	return out
}
