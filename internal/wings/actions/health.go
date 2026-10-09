package actions

import (
	"context"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/doctor"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/server"
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
}

// Report is node.health's answer.
type Report struct {
	Host      host.Stats      `json:"host"`
	DiskTotal int64           `json:"disk_total"` // the volumes' filesystem
	DiskFree  int64           `json:"disk_free"`
	Servers   []ServerUse     `json:"servers"`
	Checks    []doctor.Result `json:"checks"`
	CheckedAt int64           `json:"checked_at,omitempty"` // unix ms; 0 if never
	Checking  bool            `json:"checking"`
}

// Report is the machine now, and the last checks.
func (h *Health) Report() Report {
	r := Report{Host: host.ReadStats()}
	if h.Space != nil {
		r.DiskTotal, r.DiskFree, _ = h.Space(h.Volumes)
	}
	if h.Servers != nil {
		for id, st := range h.Servers.List() {
			u := ServerUse{ID: id, State: string(st)}
			if h.Metrics != nil {
				p := h.Metrics.Latest(id)
				u.CPU, u.Memory, u.Disk = p.CPUAvg, p.MemoryAvg, p.DiskBytes
			}
			r.Servers = append(r.Servers, u)
		}
	}
	h.mu.Lock()
	r.Checks, r.Checking = h.results, h.running
	if !h.ranAt.IsZero() {
		r.CheckedAt = h.ranAt.UnixMilli()
	}
	h.mu.Unlock()
	return r
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
