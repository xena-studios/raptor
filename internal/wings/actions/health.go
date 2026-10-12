package actions

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/doctor"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/system"
)

// Node health, for the Panel's node page (docs/WINGS.md#doctor): the
// machine's resources and its servers' use of them now (node.health), and
// `raptor doctor`'s checks, run in the background on request (node.doctor)
// since they take up to a minute.
const (
	NodeHealth = "node.health"
	NodeDoctor = "node.doctor"
)

// Health runs the doctor's checks and keeps the last results.
type Health struct {
	// ConfigPath is Wings' config.yml.
	ConfigPath string
	// Volumes is where servers' files live (its disk is reported).
	Volumes string
	// Space returns a path's filesystem size and free bytes.
	Space   func(path string) (total, free int64, err error)
	Servers interface {
		List() map[string]server.State
	}
	Metrics interface{ Latest(id string) metrics.Point }
	// Disk is the guard watching Wings' and Docker's directories; Labels
	// says what each path holds, for people.
	Disk   *host.DiskGuard
	Labels map[string]string
	// Data lists the data disks (system.DiskManager.Data); mounted ones
	// are in the report's disks.
	Data func(ctx context.Context) []system.DataDisk
	// Run runs the checks (doctor.Run on doctor.LocalEnv by default).
	Run func(ctx context.Context) []doctor.Result

	mu      sync.Mutex
	running bool
	results []doctor.Result
	ranAt   time.Time
}

func (h *Health) run(ctx context.Context) []doctor.Result {
	if h.Run != nil {
		return h.Run(ctx)
	}
	e, closeEnv := doctor.LocalEnv(ctx, h.ConfigPath)
	defer closeEnv()
	return doctor.Run(ctx, e)
}

// Start runs the checks in the background, unless they're running.
func (h *Health) Start() {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	h.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		results := h.run(ctx)
		h.mu.Lock()
		h.results, h.ranAt, h.running = results, time.Now(), false
		h.mu.Unlock()
	}()
}

// ServerUse is one server's use of the machine right now.
type ServerUse struct {
	ID     string  `json:"id"`
	State  string  `json:"state"`
	CPU    float64 `json:"cpu"`    // percent of one core
	Memory int64   `json:"memory"` // bytes
	Disk   int64   `json:"disk"`   // bytes
	Rx     int64   `json:"rx"`     // bytes per second
	Tx     int64   `json:"tx"`
	// Players online, if the game answers player queries.
	Players *int `json:"players,omitempty"`
}

// serverUses is what each server uses now.
func serverUses(list map[string]server.State, m interface{ Latest(id string) metrics.Point }) []ServerUse {
	out := make([]ServerUse, 0, len(list))
	for id, st := range list {
		u := ServerUse{ID: id, State: string(st)}
		if m != nil {
			p := m.Latest(id)
			u.CPU, u.Memory, u.Disk, u.Rx, u.Tx = p.CPUAvg, p.MemoryAvg, p.DiskBytes, p.RxBytes, p.TxBytes
			if p.PlayersMax != nil {
				n := *p.PlayersMax
				u.Players = &n
			}
		}
		out = append(out, u)
	}
	slices.SortFunc(out, func(a, b ServerUse) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Disk is a filesystem and what Raptor keeps on it.
type Disk struct {
	Paths []DiskPath `json:"paths"`
	Total int64      `json:"total"`
	Free  int64      `json:"free"`
}

// DiskPath is a directory Raptor uses.
type DiskPath struct {
	Path  string `json:"path"`
	Label string `json:"label,omitempty"`
}

// Report is node.health's answer.
type Report struct {
	Host      host.Stats      `json:"host"`
	Details   host.Details    `json:"details"`
	DiskTotal int64           `json:"disk_total"` // the volumes' filesystem
	DiskFree  int64           `json:"disk_free"`
	Disks     []Disk          `json:"disks"`
	Servers   []ServerUse     `json:"servers"`
	Checks    []doctor.Result `json:"checks"`
	CheckedAt int64           `json:"checked_at,omitempty"` // unix ms; 0 if never
	Checking  bool            `json:"checking"`
}

// Report is the machine now, and the last checks.
func (h *Health) Report() Report {
	r := Report{Host: host.ReadStats(), Details: host.ReadDetails()}
	if h.Space != nil {
		r.DiskTotal, r.DiskFree, _ = h.Space(h.Volumes)
		spaces := h.Disk.Spaces()
		if r.DiskTotal > 0 {
			spaces = append([]host.DiskSpace{{Path: h.Volumes, Total: r.DiskTotal, Free: r.DiskFree}}, spaces...)
		}
		labels := h.Labels
		if h.Data != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			spaces, labels = withDataDisks(spaces, labels, h.Data(ctx))
			cancel()
		}
		r.Disks = groupDisks(spaces, labels)
	}
	if h.Servers != nil {
		r.Servers = serverUses(h.Servers.List(), h.Metrics)
	}
	h.mu.Lock()
	r.Checks, r.Checking = h.results, h.running
	if !h.ranAt.IsZero() {
		r.CheckedAt = h.ranAt.UnixMilli()
	}
	h.mu.Unlock()
	return r
}

// withDataDisks adds the mounted data disks to the disks measured.
func withDataDisks(spaces []host.DiskSpace, labels map[string]string, data []system.DataDisk) ([]host.DiskSpace, map[string]string) {
	out := maps.Clone(labels)
	if out == nil {
		out = map[string]string{}
	}
	for _, d := range data {
		if d.ID == system.MainDisk || !d.Mounted || d.Total == 0 {
			continue
		}
		spaces = append(spaces, host.DiskSpace{Path: d.Mountpoint, Total: d.Total, Free: d.Free})
		what := "servers' files"
		if d.Role == storage.RoleBackups {
			what = "backups"
		}
		out[d.Mountpoint] = fmt.Sprintf("Data disk %s (%s)", d.ID, what)
	}
	return spaces, out
}

// ServerStorage is the space for servers' files: the main volume and the
// data disks for servers.
func ServerStorage(data []system.DataDisk) int64 {
	var total int64
	for _, d := range data {
		if d.Role == storage.RoleServers && d.Mounted {
			total += d.Total
		}
	}
	return total
}

// groupDisks puts paths on the same filesystem together. Paths measured at
// the same moment with the same size and free space are on the same one.
func groupDisks(spaces []host.DiskSpace, labels map[string]string) []Disk {
	var out []Disk
	for _, sp := range spaces {
		p := DiskPath{Path: sp.Path, Label: labels[sp.Path]}
		i := slices.IndexFunc(out, func(d Disk) bool { return d.Total == sp.Total && d.Free == sp.Free })
		switch {
		case i < 0:
			out = append(out, Disk{Paths: []DiskPath{p}, Total: sp.Total, Free: sp.Free})
		case !slices.ContainsFunc(out[i].Paths, func(x DiskPath) bool { return x.Path == sp.Path }):
			out[i].Paths = append(out[i].Paths, p)
		}
	}
	return out
}

// RegisterHealth adds node.health and node.doctor. Neither changes
// anything; the first report starts the checks.
func RegisterHealth(x *command.Executor, h *Health) {
	x.Register(NodeHealth, command.Handler{Signed: command.Never, Run: func(context.Context, command.Envelope) (any, error) {
		h.mu.Lock()
		never := h.ranAt.IsZero() && !h.running
		h.mu.Unlock()
		if never {
			h.Start()
		}
		return h.Report(), nil
	}})
	x.Register(NodeDoctor, command.Handler{Signed: command.Never, Run: func(context.Context, command.Envelope) (any, error) {
		h.Start()
		return h.Report(), nil
	}})
}
