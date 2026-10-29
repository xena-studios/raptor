// Package databases runs database instances on the node and the databases
// on them for servers (docs/DATABASES.md): MariaDB, MySQL, and PostgreSQL
// in containers on the node's networks, each database with its own user.
package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/networks"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Role is the role label of instances' containers.
const Role = "database"

// ContainerName is an instance's container.
func ContainerName(id string) string { return "raptor-db-" + id }

// Bounds.
const (
	MaxInstances = 20
	MinMemoryMiB = 256
)

// Limits are an instance's resource limits.
type Limits struct {
	MemoryMiB  int64 `json:"memory_mib"`
	CPUPercent int64 `json:"cpu_percent,omitempty"` // 100 = one core; 0 = none
	DiskMiB    int64 `json:"disk_mib,omitempty"`    // 0 = none
}

// Settings are the engine settings owners change most.
type Settings struct {
	MaxConnections int    `json:"max_connections,omitempty"` // 0 = 100
	Timezone       string `json:"timezone,omitempty"`        // "" = the node's
	// Maintenance is when its maintenance window starts each day, in the
	// node's time ("04:00"; "" is DefaultMaintenance), or "off" for no
	// patch updates but by hand.
	Maintenance string `json:"maintenance,omitempty"`
	// Advanced are any other settings on the engine's safe list, as
	// name = value lines (settings.go).
	Advanced string `json:"advanced,omitempty"`
}

// Instance is a database instance.
type Instance struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Image   string `json:"image"`
	// Networks are the owner's networks it's on; none is the server
	// network.
	Networks     []string `json:"networks"`
	Limits       Limits   `json:"limits"`
	Settings     Settings `json:"settings"`
	DesiredState string   `json:"desired_state"`
	CreatedAt    int64    `json:"created_at"`

	// Reported, never saved.
	State     string            `json:"state"` // starting, running, stopped, failed
	Error     string            `json:"error,omitempty"`
	Host      string            `json:"host"` // its name on its networks
	Port      int               `json:"port"`
	Databases int               `json:"databases"`
	Addresses map[string]string `json:"addresses"` // its fixed address on each network, by network ID
	// Update: this Wings has a newer patch of its version.
	Update bool `json:"update,omitempty"`
	// Previous is the version it can go back to, with the data from
	// before its last version change, until GoBackUntil (unix seconds).
	Previous    string  `json:"previous,omitempty"`
	GoBackUntil int64   `json:"go_back_until,omitempty"`
	Change      *Change `json:"change,omitempty"`
	// External is its port on the node, if it has external access.
	External *External `json:"external,omitempty"`
}

// Database is one database on an instance. It belongs to the instance;
// servers use it through links (DECISIONS #342).
type Database struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	// ServerID is the server it's seen from (a server's db.* commands), or
	// else the first server linked to it; empty with none.
	ServerID string `json:"server_id,omitempty"`
	Label    string `json:"label"`
	Name     string `json:"name"` // on the instance
	User     string `json:"user"`
	// DBName and Username are Name and User again, under node.db.databases'
	// names.
	DBName    string `json:"db_name"`
	Username  string `json:"username"`
	Engine    string `json:"engine"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	CreatedAt int64  `json:"created_at"`
	// ReadOnlyUser is its read-only user, if it has one.
	ReadOnlyUser string `json:"readonly_user,omitempty"`
	// Links are the servers using it, in the order they were linked.
	Links []Link `json:"links"`
}

// Link is a server using a database: it gets the database's connection
// details as environment variables, its backups hold it, and its db.*
// commands reach it.
type Link struct {
	ServerID  string `json:"server_id"`
	CreatedAt int64  `json:"created_at"`
}

// States.
const (
	StateStarting = "starting"
	StateRunning  = "running"
	StateStopped  = "stopped"
	StateFailed   = "failed"
	// StateUpgrading: changing version; servers can't reach it for a while.
	StateUpgrading = "upgrading"
)

// Errors, matched with errors.Is. Their messages are sentences for people.
var (
	ErrInvalid  = errors.New("invalid database settings")
	ErrNotFound = errors.New("no such database instance or database")
	ErrNotReady = errors.New("database instance isn't running")
)

type invalid struct{ msg string }

func (e invalid) Error() string        { return e.msg }
func (e invalid) Is(target error) bool { return target == ErrInvalid }

type notReady struct{ msg string }

func (e notReady) Error() string        { return e.msg }
func (e notReady) Is(target error) bool { return target == ErrNotReady }

// Runtime is what the service needs of the container runtime.
type Runtime interface {
	EnsureService(ctx context.Context, s containers.ServiceSpec) error
	StopService(ctx context.Context, name string, timeout time.Duration) error
	RemoveService(ctx context.Context, name string) error
	ServiceState(ctx context.Context, name string) (containers.State, bool, error)
	Exec(ctx context.Context, name string, s containers.ExecSpec) (int, error)
	ServiceLogs(ctx context.Context, name string, lines int) (string, error)
	Stats(ctx context.Context, name string) (containers.Stats, error)
}

// Networks resolves the owner's networks.
type Networks interface {
	Resolve(ctx context.Context, ids []string) ([]containers.Network, error)
}

// Options configure the service.
type Options struct {
	Store   *store.DB
	Runtime Runtime
	Log     *slog.Logger
	// Volume is the server data volume, and Dir the folder on it that
	// instances' folders go in (<Dir>/<id>).
	Volume *storage.Volume
	Dir    string
	// KeyPath is the database key, which seals passwords in state.db.
	KeyPath  string
	Networks Networks
	// Timezone is the node's time zone, for instances that don't set one.
	Timezone func() string
	// Jobs runs imports and exports; without it there are none.
	Jobs *jobs.Engine
	// Changed, if set, is called when the firewall's guards or instances'
	// external ports changed.
	Changed func(ctx context.Context, guards []firewall.Guard, external []firewall.External) error
	// StartTimeout bounds how long an instance has to answer after it
	// starts (3 minutes by default; a first start initializes it).
	StartTimeout time.Duration
	// Hostname is the node's name in DNS, which instances' certificates
	// name; ServerNetwork the server network, which instances on no
	// network of the owner's are on; ReservedPorts Wings' own ports; and
	// CheckPort, if set, fails if a port is already bound on the node.
	Hostname      string
	ServerNetwork containers.Network
	ReservedPorts []int
	CheckPort     func(port int) error
	// Kernel is the host's kernel release (/proc/sys/kernel/osrelease),
	// for versions that don't run on some; nil is one they all run on.
	Kernel func() string
	// FreeSpace returns the free bytes on the filesystem holding a path,
	// checked before a version change copies an instance's data. nil = no
	// check.
	FreeSpace func(path string) (int64, error)
	// Events records instances going down and coming back, and version
	// changes ending (health.go); nil records none.
	Events *events.Outbox
}

// Service runs the instances and their databases.
type Service struct {
	o      Options
	sealer *sealer

	mu      sync.Mutex
	status  map[string]status // by instance ID
	ipv6    map[string]map[string]netip.Addr
	locks   map[string]*sync.Mutex
	files   Files
	backups Backups
	changes map[string]*Change // by instance ID

	transfers transfers
	health    health
	runs      queryRuns // streamed queries (stream.go)
}

type status struct{ state, err string }

// New returns the service. Call Start once the runtime is set up.
func New(o Options) (*Service, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Timezone == nil {
		o.Timezone = func() string { return "" }
	}
	if o.StartTimeout == 0 {
		o.StartTimeout = 3 * time.Minute
	}
	s, err := loadKey(o.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("database key: %w", err)
	}
	svc := &Service{
		o: o, sealer: s, status: map[string]status{}, ipv6: map[string]map[string]netip.Addr{},
		locks: map[string]*sync.Mutex{}, changes: map[string]*Change{},
	}
	svc.registerJobs()
	return svc, nil
}

// lock serializes changes to one instance.
func (s *Service) lock(id string) func() {
	s.mu.Lock()
	l, ok := s.locks[id]
	if !ok {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *Service) setStatus(id, state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := status{state: state}
	if err != nil {
		st.err = err.Error()
	}
	s.status[id] = st
}

func (s *Service) dir(id string) string { return filepath.Join(s.o.Dir, id) }

// Start brings every instance to its desired state, in the background,
// and drops the databases of servers that are gone.
func (s *Service) Start(ctx context.Context) error {
	rows, err := s.o.Store.Read.ListInstances(ctx)
	if err != nil {
		return err
	}
	s.loadDown(ctx)
	for _, r := range rows {
		s.recover(ctx, r.ID)
		if r.DesiredState == StateRunning {
			s.setStatus(r.ID, StateStarting, nil)
			go s.ensure(context.WithoutCancel(ctx), r.ID)
		} else {
			s.setStatus(r.ID, StateStopped, nil)
		}
	}
	go s.pruneLinks(context.WithoutCancel(ctx))
	go s.loop(ctx)
	go s.watch(ctx)
	return nil
}

// instance reads an instance's row.
func (s *Service) instance(ctx context.Context, q *store.Queries, id string) (Instance, store.DatabaseInstance, error) {
	r, err := q.GetInstance(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Instance{}, r, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Instance{}, r, err
	}
	in, err := fromRow(r)
	return in, r, err
}

func fromRow(r store.DatabaseInstance) (Instance, error) {
	in := Instance{
		ID: r.ID, Name: r.Name, Engine: r.Engine, Version: r.Version, Image: r.Image,
		DesiredState: r.DesiredState, CreatedAt: r.CreatedAt, Port: port(r.Engine),
	}
	if err := json.Unmarshal([]byte(r.Networks), &in.Networks); err != nil {
		return in, fmt.Errorf("instance %s networks: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(r.Limits), &in.Limits); err != nil {
		return in, fmt.Errorf("instance %s limits: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(r.Settings), &in.Settings); err != nil {
		return in, fmt.Errorf("instance %s settings: %w", r.ID, err)
	}
	if in.Networks == nil {
		in.Networks = []string{}
	}
	in.Host = hostName(in.ID, in.Name)
	if l := latest(r.Engine, r.Version); l != "" && l != r.Image {
		in.Update = true
	}
	if r.PrevVersion != "" && r.RollbackUntil > time.Now().Unix() {
		in.Previous, in.GoBackUntil = r.PrevVersion, r.RollbackUntil
	}
	return in, nil
}

// hostName is an instance's name on its networks.
func hostName(id, name string) string {
	if slug := server.Slug(name); slug != "" {
		return slug + ".db.raptor"
	}
	return server.ShortID(id) + ".db.raptor"
}

func aliases(id, name string) []string {
	out := []string{hostName(id, name)}
	if short := server.ShortID(id) + ".db.raptor"; short != out[0] {
		out = append(out, short)
	}
	return out
}

// ensure brings one instance up: its folder and quota, its container on
// its networks, and waits for it to answer.
func (s *Service) ensure(ctx context.Context, id string) {
	defer s.lock(id)()
	if err := s.ensureLocked(ctx, id); err != nil {
		s.o.Log.Error("database instance failed to start", "instance", id, "err", err)
		s.setStatus(id, StateFailed, err)
		if ctx.Err() == nil {
			s.markDown(ctx, id, err.Error())
		}
		return
	}
	s.markUp(ctx, id)
	s.setStatus(id, StateRunning, nil)
	s.changed(ctx)
}

func (s *Service) ensureLocked(ctx context.Context, id string) error {
	in, r, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return err
	}
	if in.DesiredState != StateRunning {
		return nil
	}
	s.setStatus(id, StateStarting, nil)
	root, err := s.sealer.open(r.RootPassword)
	if err != nil {
		return err
	}
	dir := s.dir(id)
	if err := s.prepare(ctx, id, dir, uint32(r.QuotaProject.Int64), in.Limits.DiskMiB); err != nil { //nolint:gosec // allocated by Wings from 1000 up
		return err
	}
	nets, err := s.o.Networks.Resolve(ctx, in.Networks)
	if err != nil {
		return err
	}
	addrs, err := s.pin(ctx, id, in.Networks)
	if err != nil {
		return err
	}
	spec := serviceSpec(in, dir, root, s.o.Timezone())
	spec.Networks, spec.Addresses, spec.Aliases = nets, addrs, aliases(in.ID, in.Name)
	if isRedis(in.Engine) {
		if err := writeACL(dir, root); err != nil {
			return err
		}
	}
	port, _ := external(r)
	if port != 0 && hasExternal(in.Engine) {
		if _, err := ensureTLS(dir, in.Name, s.tlsNames(in)); err != nil {
			return err
		}
		externalSpec(&spec, in, dir, port)
	}
	if err := s.o.Runtime.EnsureService(ctx, spec); err != nil {
		return err
	}
	if st, ok, err := s.o.Runtime.ServiceState(ctx, ContainerName(id)); err == nil && ok {
		s.mu.Lock()
		s.ipv6[id] = st.IPv6
		s.mu.Unlock()
	}
	// A first start initializes the data folder, which takes a while.
	deadline := time.Now().Add(s.o.StartTimeout)
	restarts := -1
	if st, ok, err := s.o.Runtime.ServiceState(ctx, ContainerName(id)); err == nil && ok {
		restarts = st.Restarts
	}
	for {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := ping(pctx, in, dir, root)
		cancel()
		if err == nil {
			// MariaDB and MySQL's accounts for external access, or its
			// absence (external.go).
			internal, err := s.internal(ctx, in)
			if err != nil {
				return err
			}
			if err := s.syncAccounts(ctx, in, root, port != 0, internal); err != nil {
				return fmt.Errorf("setting up its users: %w", err)
			}
			s.started(ctx, id)
			return nil
		}
		st, ok, serr := s.o.Runtime.ServiceState(ctx, ContainerName(id))
		// Docker restarts one that exits, so one that keeps failing is
		// seen by its restarts more than by its being stopped.
		if serr == nil && ok && restarts >= 0 && st.Restarts > restarts+1 {
			return fmt.Errorf("it keeps stopping (exit code %d)%s%s", st.ExitCode, oom(st.OOMKilled), s.lastLog(ctx, id))
		}
		if serr == nil && ok && !st.Running && !st.StartedAt.IsZero() && time.Since(st.StartedAt) > 5*time.Second {
			return fmt.Errorf("it stopped (exit code %d)%s%s", st.ExitCode, oom(st.OOMKilled), s.lastLog(ctx, id))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("it didn't answer within %s: %w%s", s.o.StartTimeout, err, s.lastLog(ctx, id))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func oom(killed bool) string {
	if killed {
		return ", out of memory: give it more"
	}
	return ""
}

// prepare makes an instance's folders, under its quota project.
func (s *Service) prepare(ctx context.Context, id, dir string, project uint32, diskMiB int64) error {
	if s.o.Volume != nil {
		if err := s.o.Volume.Prepare(dir, project, diskMiB<<20, 0, 0); err != nil {
			return err
		}
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // root only: it holds the engine's socket
		return err
	}
	for _, d := range []string{dataDir(dir), runDir(dir)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		if err := os.Chown(d, engineUID, engineUID); err != nil {
			return err
		}
	}
	// Explicit modes: Wings runs with umask 077 (UMask in its unit), and
	// the engine's user reads this folder through a bind mount.
	if err := os.MkdirAll(confDir(dir), 0o755); err != nil { //nolint:gosec // read by the engine's user
		return err
	}
	if err := os.Chmod(confDir(dir), 0o755); err != nil { //nolint:gosec // read by the engine's user
		return err
	}
	return s.writeHBA(ctx, id, dir)
}

// writeHBA writes PostgreSQL's pg_hba.conf for an instance: the fixed
// lines and one per read-only user, and with external access, TLS required
// from anywhere but its networks.
func (s *Service) writeHBA(ctx context.Context, id, dir string) error {
	in, r, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return err
	}
	if in.Engine != Postgres {
		return nil
	}
	port, _ := external(r)
	var internal []containers.Network
	if port != 0 {
		if internal, err = s.internal(ctx, in); err != nil {
			return err
		}
	}
	dbs, err := s.o.Store.Read.ListDatabases(ctx)
	if err != nil {
		return err
	}
	var ro [][2]string
	for _, d := range dbs {
		if d.InstanceID == id && d.RoUsername != "" {
			ro = append(ro, [2]string{d.DbName, d.RoUsername})
		}
	}
	p := filepath.Join(confDir(dir), "pg_hba.conf")
	if err := os.WriteFile(p, []byte(pgHBA(ro, port != 0, internal)), 0o644); err != nil { //nolint:gosec // the engine reads it
		return err
	}
	return os.Chmod(p, 0o644) //nolint:gosec // the engine reads it, whatever Wings' umask
}

// pin gives an instance a fixed address on each of the owner's networks
// it's on, by Docker network name.
func (s *Service) pin(ctx context.Context, id string, ids []string) (map[string]netip.Addr, error) {
	out := map[string]netip.Addr{}
	err := s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		all, err := q.ListInstanceAddresses(ctx)
		if err != nil {
			return err
		}
		taken, err := networks.Taken(ctx, q)
		if err != nil {
			return err
		}
		mine := map[string]netip.Addr{}
		for _, a := range all {
			if ip, err := netip.ParseAddr(a.Ip); err == nil && a.InstanceID == id {
				mine[a.NetworkID] = ip
			}
		}
		for nid := range mine {
			if !slices.Contains(ids, nid) {
				if err := q.DeleteInstanceAddress(ctx, store.DeleteInstanceAddressParams{InstanceID: id, NetworkID: nid}); err != nil {
					return err
				}
			}
		}
		for _, nid := range ids {
			ip, ok := mine[nid]
			if !ok {
				n, err := q.GetNetwork(ctx, nid)
				if err != nil {
					return fmt.Errorf("network %s: %w", nid, err)
				}
				subnet, err := netip.ParsePrefix(n.Subnet)
				if err != nil {
					return err
				}
				if ip, err = networks.FreeAddress(subnet, taken[nid]); err != nil {
					return err
				}
				if err := q.InsertInstanceAddress(ctx, store.InsertInstanceAddressParams{InstanceID: id, NetworkID: nid, Ip: ip.String()}); err != nil {
					return err
				}
			}
			out[networks.DockerName(nid)] = ip
		}
		return nil
	})
	return out, err
}

// InstanceInput is what saving an instance takes: no ID creates one.
type InstanceInput struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	Engine   string   `json:"engine,omitempty"`  // creating only
	Version  string   `json:"version,omitempty"` // creating only; "" is the engine's default
	Networks []string `json:"networks"`
	Limits   Limits   `json:"limits"`
	Settings Settings `json:"settings"`
}

func (in *InstanceInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 40 || strings.ContainsFunc(in.Name, func(r rune) bool { return r < ' ' }) {
		return invalid{"an instance's name is 1 to 40 characters"}
	}
	if in.Limits.MemoryMiB < MinMemoryMiB {
		return invalid{fmt.Sprintf("an instance needs at least %d MiB of memory", MinMemoryMiB)}
	}
	if in.Limits.CPUPercent < 0 || in.Limits.DiskMiB < 0 {
		return invalid{"limits can't be negative"}
	}
	if in.Limits.DiskMiB != 0 && in.Limits.DiskMiB < 512 {
		return invalid{"a disk limit is at least 512 MiB, or none"}
	}
	if c := in.Settings.MaxConnections; c != 0 && (c < 10 || c > 2000) {
		return invalid{"max connections is between 10 and 2000"}
	}
	if tz := in.Settings.Timezone; tz != "" {
		if _, err := time.LoadLocation(tz); err != nil || tz == "Local" {
			return invalid{fmt.Sprintf("%q isn't a time zone, like Europe/Berlin", tz)}
		}
	}
	if m := in.Settings.Maintenance; m != "" && m != "off" && !maintenanceRe.MatchString(m) {
		return invalid{"the maintenance window starts on the hour, like 04:00, or is off"}
	}
	in.Settings.Advanced = strings.TrimSpace(strings.ReplaceAll(in.Settings.Advanced, "\r\n", "\n"))
	if len(in.Networks) > 8 {
		return invalid{"an instance is on at most 8 networks"}
	}
	for i, id := range in.Networks {
		if id == "" || slices.Contains(in.Networks[:i], id) {
			return invalid{"each network is listed once"}
		}
	}
	return nil
}

// Save creates an instance (no ID) or changes one: its name, networks,
// limits, and settings. A change that changes its container restarts it.
func (s *Service) Save(ctx context.Context, in InstanceInput) (Instance, error) {
	if err := in.validate(); err != nil {
		return Instance{}, err
	}
	if in.Networks == nil {
		in.Networks = []string{}
	}
	if _, err := s.o.Networks.Resolve(ctx, in.Networks); err != nil {
		return Instance{}, err
	}
	rows, err := s.o.Store.Read.ListInstances(ctx)
	if err != nil {
		return Instance{}, err
	}
	for _, r := range rows {
		if r.ID != in.ID && strings.EqualFold(r.Name, in.Name) {
			return Instance{}, invalid{fmt.Sprintf("there's already an instance called %s", r.Name)}
		}
	}
	now := time.Now().Unix()
	if in.ID == "" {
		return s.create(ctx, in, len(rows), now)
	}

	if err := s.busy(in.ID); err != nil {
		return Instance{}, err
	}
	defer s.lock(in.ID)()
	cur, r, err := s.instance(ctx, s.o.Store.Read, in.ID)
	if err != nil {
		return Instance{}, err
	}
	if _, err := parseAdvanced(cur.Engine, in.Settings.Advanced, in.Limits.MemoryMiB); err != nil {
		return Instance{}, err
	}
	if err := s.o.Store.Write.UpdateInstance(ctx, store.UpdateInstanceParams{
		Name: in.Name, Networks: mustJSON(in.Networks), Limits: mustJSON(in.Limits), Settings: mustJSON(in.Settings),
		UpdatedAt: now, ID: in.ID,
	}); err != nil {
		return Instance{}, err
	}
	// A new disk limit applies at once.
	if in.Limits.DiskMiB != cur.Limits.DiskMiB && s.o.Volume != nil && r.QuotaProject.Valid {
		if err := s.o.Volume.SetLimit(uint32(r.QuotaProject.Int64), in.Limits.DiskMiB<<20); err != nil { //nolint:gosec // allocated by Wings
			s.o.Log.Warn("applying an instance's disk limit failed", "instance", in.ID, "err", err)
		}
	}
	if cur.DesiredState == StateRunning {
		s.setStatus(in.ID, StateStarting, nil)
		go s.ensure(context.WithoutCancel(ctx), in.ID)
	}
	return s.Get(ctx, in.ID)
}

func (s *Service) create(ctx context.Context, in InstanceInput, count int, now int64) (Instance, error) {
	if count >= MaxInstances {
		return Instance{}, invalid{fmt.Sprintf("a node has at most %d database instances", MaxInstances)}
	}
	v, err := lookupOn(in.Engine, in.Version, s.kernel())
	if err != nil {
		return Instance{}, err
	}
	if _, err := parseAdvanced(v.Engine, in.Settings.Advanced, in.Limits.MemoryMiB); err != nil {
		return Instance{}, err
	}
	uid, err := uuid.NewV7()
	if err != nil {
		return Instance{}, err
	}
	id := uid.String()
	root, err := password()
	if err != nil {
		return Instance{}, err
	}
	sealed, err := s.sealer.seal(root)
	if err != nil {
		return Instance{}, err
	}
	err = s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		project, err := q.NextQuotaProject(ctx)
		if err != nil {
			return err
		}
		return q.InsertInstance(ctx, store.InsertInstanceParams{
			ID: id, Name: in.Name, Engine: v.Engine, Version: v.Version, Image: v.Image, RootPassword: sealed,
			Networks: mustJSON(in.Networks), Limits: mustJSON(in.Limits), Settings: mustJSON(in.Settings),
			DesiredState: StateRunning, QuotaProject: sql.NullInt64{Int64: project, Valid: true},
			CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		return Instance{}, err
	}
	s.setStatus(id, StateStarting, nil)
	go s.ensure(context.WithoutCancel(ctx), id)
	return s.Get(ctx, id)
}

// Get returns one instance.
func (s *Service) Get(ctx context.Context, id string) (Instance, error) {
	list, err := s.List(ctx)
	if err != nil {
		return Instance{}, err
	}
	for _, in := range list {
		if in.ID == id {
			return in, nil
		}
	}
	return Instance{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// List returns the instances, with their state.
func (s *Service) List(ctx context.Context) ([]Instance, error) {
	rows, err := s.o.Store.Read.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	dbs, err := s.o.Store.Read.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	addrs, err := s.o.Store.Read.ListInstanceAddresses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Instance, 0, len(rows))
	for _, r := range rows {
		in, err := fromRow(r)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		st := s.status[r.ID]
		if c := s.changes[r.ID]; c != nil {
			cp := *c
			in.Change = &cp
		}
		s.mu.Unlock()
		in.State, in.Error = st.state, st.err
		// Restarting on the new version is part of the change.
		if in.Change != nil && !in.Change.Done && in.State == StateStarting {
			in.State = StateUpgrading
		}
		if in.State == "" {
			in.State = StateStopped
		}
		for _, d := range dbs {
			if d.InstanceID == r.ID {
				in.Databases++
			}
		}
		s.reportExternal(&in, r)
		in.Addresses = map[string]string{}
		for _, a := range addrs {
			if a.InstanceID == r.ID {
				in.Addresses[a.NetworkID] = a.Ip
			}
		}
		out = append(out, in)
	}
	return out, nil
}

// Power starts, stops, or restarts an instance.
func (s *Service) Power(ctx context.Context, id, action string) error {
	if action != "start" && action != "stop" && action != "restart" {
		return invalid{"start, stop, or restart"}
	}
	if err := s.busy(id); err != nil {
		return err
	}
	unlock := s.lock(id)
	if _, _, err := s.instance(ctx, s.o.Store.Read, id); err != nil {
		unlock()
		return err
	}
	desired := StateRunning
	if action == "stop" {
		desired = StateStopped
	}
	if err := s.o.Store.Write.SetInstanceDesired(ctx, store.SetInstanceDesiredParams{DesiredState: desired, UpdatedAt: time.Now().Unix(), ID: id}); err != nil {
		unlock()
		return err
	}
	if action != "start" {
		if err := s.o.Runtime.StopService(ctx, ContainerName(id), 2*time.Minute); err != nil {
			unlock()
			return err
		}
		s.setStatus(id, StateStopped, nil)
	}
	if action == "stop" {
		s.forget(ctx, id) // a restart that answers is "back up"
	}
	unlock()
	if action != "stop" {
		s.setStatus(id, StateStarting, nil)
		go s.ensure(context.WithoutCancel(ctx), id)
	}
	return nil
}

// Delete removes an instance with every database on it, and its files.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.busy(id); err != nil {
		return err
	}
	defer s.lock(id)()
	_, r, err := s.instance(ctx, s.o.Store.Read, id)
	if err != nil {
		return err
	}
	if err := s.o.Runtime.RemoveService(ctx, ContainerName(id)); err != nil {
		return err
	}
	if err := os.RemoveAll(s.dir(id)); err != nil {
		return fmt.Errorf("removing its files: %w", err)
	}
	if s.o.Volume != nil && r.QuotaProject.Valid {
		_ = s.o.Volume.Release(uint32(r.QuotaProject.Int64)) //nolint:gosec // allocated by Wings
	}
	if err := s.o.Store.Write.DeleteInstance(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.status, id)
	delete(s.ipv6, id)
	delete(s.changes, id)
	s.mu.Unlock()
	s.forget(ctx, id)
	s.changed(ctx)
	return nil
}

// serverNetworks returns the networks a server is on: none is the server
// network.
func (s *Service) serverNetworks(ctx context.Context, serverID string) ([]string, error) {
	r, err := s.o.Store.Read.GetServer(ctx, serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: server %s", ErrNotFound, serverID)
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	if r.Networks != "" {
		if err := json.Unmarshal([]byte(r.Networks), &ids); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// shares reports whether a server and an instance have a network in
// common; none on either side is the server network.
func shares(server, instance []string) bool {
	if len(server) == 0 || len(instance) == 0 {
		return len(server) == 0 && len(instance) == 0
	}
	return slices.ContainsFunc(server, func(id string) bool { return slices.Contains(instance, id) })
}

// Usable returns the instances a server can have databases on: those on a
// network it's on.
func (s *Service) Usable(ctx context.Context, serverID string) ([]Instance, error) {
	nets, err := s.serverNetworks(ctx, serverID)
	if err != nil {
		return nil, err
	}
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := []Instance{}
	for _, in := range all {
		if shares(nets, in.Networks) {
			out = append(out, in)
		}
	}
	return out, nil
}

// ServerDatabases returns the databases linked to a server.
func (s *Service) ServerDatabases(ctx context.Context, serverID string) ([]Database, error) {
	rows, err := s.o.Store.Read.ListServerDatabases(ctx, serverID)
	if err != nil {
		return nil, err
	}
	insts, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	links, err := s.links(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Database, 0, len(rows))
	for _, r := range rows {
		d := toDatabase(r, insts, links)
		d.ServerID = serverID
		out = append(out, d)
	}
	return out, nil
}

// Databases returns every database on the node, or on one instance, with
// the servers linked to each.
func (s *Service) Databases(ctx context.Context, instanceID string) ([]Database, error) {
	if instanceID != "" {
		if _, _, err := s.instance(ctx, s.o.Store.Read, instanceID); err != nil {
			return nil, err
		}
	}
	rows, err := s.o.Store.Read.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	insts, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	links, err := s.links(ctx)
	if err != nil {
		return nil, err
	}
	out := []Database{}
	for _, r := range rows {
		if instanceID == "" || r.InstanceID == instanceID {
			out = append(out, toDatabase(r, insts, links))
		}
	}
	return out, nil
}

// links are every database's links, by database ID, oldest first.
func (s *Service) links(ctx context.Context) (map[string][]Link, error) {
	rows, err := s.o.Store.Read.ListDatabaseLinks(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]Link{}
	for _, l := range rows {
		out[l.DatabaseID] = append(out[l.DatabaseID], Link{ServerID: l.ServerID, CreatedAt: l.CreatedAt})
	}
	return out, nil
}

func toDatabase(r store.Database, insts []Instance, links map[string][]Link) Database {
	d := Database{
		ID: r.ID, InstanceID: r.InstanceID, Label: r.Label, Name: r.DbName, User: r.Username,
		DBName: r.DbName, Username: r.Username, CreatedAt: r.CreatedAt, ReadOnlyUser: r.RoUsername,
		Links: append([]Link{}, links[r.ID]...),
	}
	if len(d.Links) > 0 {
		d.ServerID = d.Links[0].ServerID
	}
	for _, in := range insts {
		if in.ID == r.InstanceID {
			d.Engine, d.Host, d.Port = in.Engine, in.Host, in.Port
		}
	}
	return d
}

// MaxDatabases bounds the databases linked to one server.
const MaxDatabases = 10

// Labels are up to 18 characters, so a database's name and user fit in
// MySQL's 32-character user names.
var labelRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,17}$`)

// serverStyle matches the names of databases made for a server,
// s<its short ID>_<label>, and their read-only users and readers (r…,
// v…): an instance's own databases can't take one.
var serverStyle = regexp.MustCompile(`^[srv][0-9a-z]{4,}_`)

const badLabel = "a database's name is up to 18 lowercase letters, digits, and underscores, starting with a letter"

// dbName is the name and user of a database made for a server on its
// instance: s<the server's short ID>_<label>. One made for the instance
// itself is named its label.
func dbName(serverID, label string) string {
	return "s" + server.ShortID(serverID) + "_" + label
}

// forServer reports whether a database was named for the server it was
// made for (dbName), rather than for its instance.
func forServer(r store.Database) bool {
	return r.DbName != r.Label && strings.HasPrefix(r.DbName, "s") && strings.HasSuffix(r.DbName, "_"+r.Label)
}

// CreateDatabase makes a database on an instance on one of a server's
// networks, linked to that server, and returns it with its password.
func (s *Service) CreateDatabase(ctx context.Context, serverID, instanceID, label string) (Database, string, error) {
	label = strings.ToLower(strings.TrimSpace(label))
	if !labelRe.MatchString(label) {
		return Database{}, "", invalid{badLabel}
	}
	nets, err := s.serverNetworks(ctx, serverID)
	if err != nil {
		return Database{}, "", err
	}
	if err := s.canLink(ctx, serverID, nets, instanceID, label); err != nil {
		return Database{}, "", err
	}
	d, pass, err := s.makeDatabase(ctx, instanceID, label, dbName(serverID, label), serverID)
	if err != nil {
		return Database{}, "", err
	}
	d.ServerID = serverID
	return d, pass, nil
}

// CreateInstanceDatabase makes a database on an instance, used by no
// server yet and named its label, and returns it with its password.
func (s *Service) CreateInstanceDatabase(ctx context.Context, instanceID, label string) (Database, string, error) {
	label = strings.ToLower(strings.TrimSpace(label))
	if !labelRe.MatchString(label) {
		return Database{}, "", invalid{badLabel}
	}
	if serverStyle.MatchString(label) || strings.HasSuffix(label, "_ro") || strings.HasSuffix(label, "_vw") {
		return Database{}, "", invalid{fmt.Sprintf("%s is a name Raptor keeps for its own users; pick another", label)}
	}
	dbs, err := s.instanceDatabases(ctx, instanceID)
	if err != nil {
		return Database{}, "", err
	}
	for _, d := range dbs {
		if d.Label == label {
			return Database{}, "", invalid{fmt.Sprintf("the instance already has a database called %s", label)}
		}
		if slices.Contains([]string{d.DbName, d.Username, d.RoUsername, d.ViewerUsername, roName(d), viewerName(d)}, label) {
			return Database{}, "", invalid{fmt.Sprintf("%s is taken on the instance; pick another name", label)}
		}
	}
	return s.makeDatabase(ctx, instanceID, label, label, "")
}

// canLink checks that a server can use a database called label on an
// instance: they share a network, the server has room for another, and
// none of its databases has that name (their variables would clash).
func (s *Service) canLink(ctx context.Context, serverID string, nets []string, instanceID, label string) error {
	in, _, err := s.instance(ctx, s.o.Store.Read, instanceID)
	if err != nil {
		return err
	}
	if !shares(nets, in.Networks) {
		if len(in.Networks) == 0 {
			return invalid{fmt.Sprintf("%s is on the default network and the server isn't: link it to a database on one of the server's networks", in.Name)}
		}
		return invalid{fmt.Sprintf("the server isn't on any network %s is on: add it to one of them first", in.Name)}
	}
	existing, err := s.o.Store.Read.ListServerDatabases(ctx, serverID)
	if err != nil {
		return err
	}
	if len(existing) >= MaxDatabases {
		return invalid{fmt.Sprintf("a server uses at most %d databases", MaxDatabases)}
	}
	if slices.ContainsFunc(existing, func(d store.Database) bool { return d.Label == label }) {
		return invalid{fmt.Sprintf("the server already uses a database called %s", label)}
	}
	return nil
}

// makeDatabase makes a database called name on an instance, linked to a server
// unless link is empty.
func (s *Service) makeDatabase(ctx context.Context, instanceID, label, name, link string) (Database, string, error) {
	if err := s.busy(instanceID); err != nil {
		return Database{}, "", err
	}
	defer s.lock(instanceID)()
	in, r, err := s.instance(ctx, s.o.Store.Read, instanceID)
	if err != nil {
		return Database{}, "", err
	}
	if s.state(instanceID) != StateRunning {
		return Database{}, "", notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	// A Redis instance holds one database: Redis can't keep two apart
	// (redis.go). Several servers can share that one.
	if isRedis(in.Engine) && slices.ContainsFunc(must(s.o.Store.Read.ListDatabases(ctx)), func(d store.Database) bool { return d.InstanceID == instanceID }) {
		return Database{}, "", invalid{fmt.Sprintf("%s already holds a database, and %s can't keep two apart: link the server to that one, or make another %[2]s instance", in.Name, engineName(in.Engine))}
	}
	root, err := s.sealer.open(r.RootPassword)
	if err != nil {
		return Database{}, "", err
	}
	pass, err := password()
	if err != nil {
		return Database{}, "", err
	}
	sealed, err := s.sealer.seal(pass)
	if err != nil {
		return Database{}, "", err
	}
	if err := createDatabase(ctx, in, s.dir(instanceID), root, name, name, pass); err != nil {
		return Database{}, "", fmt.Errorf("creating it on %s: %w", in.Name, err)
	}
	defer func() {
		// Its accounts for external access, once its row is in.
		if err := s.reconcile(context.WithoutCancel(ctx), in, r, root); err != nil {
			s.o.Log.Error("setting up a new database's users for external access failed", "instance", in.ID, "err", err)
		}
	}()
	uid, err := uuid.NewV7()
	if err != nil {
		return Database{}, "", err
	}
	row := store.Database{
		ID: uid.String(), InstanceID: instanceID, Label: label, DbName: name,
		Username: name, Password: sealed, CreatedAt: time.Now().Unix(),
	}
	err = s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.InsertDatabase(ctx, store.InsertDatabaseParams{
			ID: row.ID, InstanceID: row.InstanceID, Label: row.Label, DbName: row.DbName,
			Username: row.Username, Password: row.Password, CreatedAt: row.CreatedAt,
		}); err != nil {
			return err
		}
		if link == "" {
			return nil
		}
		return q.InsertDatabaseLink(ctx, store.InsertDatabaseLinkParams{DatabaseID: row.ID, ServerID: link, CreatedAt: row.CreatedAt})
	})
	if err != nil {
		_ = dropDatabase(context.WithoutCancel(ctx), in, s.dir(instanceID), root, name, name)
		return Database{}, "", err
	}
	s.changed(ctx)
	insts, _ := s.List(ctx)
	links, _ := s.links(ctx)
	return toDatabase(row, insts, links), pass, nil
}

// Link lets a server use a database: it must share a network with the
// database's instance. The server gets its variables at its next start.
func (s *Service) Link(ctx context.Context, id, serverID string) error {
	r, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	n, err := s.o.Store.Read.DatabaseLinked(ctx, store.DatabaseLinkedParams{DatabaseID: id, ServerID: serverID})
	if err != nil || n > 0 {
		return err // linked already: nothing to do
	}
	nets, err := s.serverNetworks(ctx, serverID)
	if err != nil {
		return err
	}
	if err := s.canLink(ctx, serverID, nets, r.InstanceID, r.Label); err != nil {
		return err
	}
	if err := s.o.Store.Write.InsertDatabaseLink(ctx, store.InsertDatabaseLinkParams{DatabaseID: id, ServerID: serverID, CreatedAt: time.Now().Unix()}); err != nil {
		return err
	}
	s.changed(ctx)
	return nil
}

// Unlink stops a server using a database. The database stays.
func (s *Service) Unlink(ctx context.Context, id, serverID string) error {
	n, err := s.o.Store.Write.DeleteDatabaseLink(ctx, store.DeleteDatabaseLinkParams{DatabaseID: id, ServerID: serverID})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: the server doesn't use that database", ErrNotFound)
	}
	s.changed(ctx)
	return nil
}

// byID reads any database, for admins.
func (s *Service) byID(ctx context.Context, id string) (store.Database, error) {
	r, err := s.o.Store.Read.GetDatabase(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return r, err
}

func (s *Service) state(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status[id].state
}

// database reads one of the databases linked to a server.
func (s *Service) database(ctx context.Context, serverID, id string) (store.Database, error) {
	r, err := s.o.Store.Read.GetDatabase(ctx, id)
	if err == nil {
		var n int64
		if n, err = s.o.Store.Read.DatabaseLinked(ctx, store.DatabaseLinkedParams{DatabaseID: id, ServerID: serverID}); err == nil && n == 0 {
			err = sql.ErrNoRows
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return r, err
}

// ErrShared: a server can't delete a database other servers use.
var ErrShared = errors.New("other servers use this database; unlink it from the Databases page instead")

// DeleteDatabase drops one of a server's databases and its users, if no
// other server uses it.
func (s *Service) DeleteDatabase(ctx context.Context, serverID, id string) error {
	if _, err := s.database(ctx, serverID, id); err != nil {
		return err
	}
	links, err := s.links(ctx)
	if err != nil {
		return err
	}
	if len(links[id]) > 1 {
		return ErrShared
	}
	return s.DeleteByID(ctx, id)
}

// DeleteByID drops a database and its users, whichever servers use it
// (their links go with it).
func (s *Service) DeleteByID(ctx context.Context, id string) error {
	r, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.drop(ctx, r); err != nil {
		return err
	}
	s.changed(ctx)
	return nil
}

func (s *Service) drop(ctx context.Context, r store.Database) error {
	if err := s.busy(r.InstanceID); err != nil {
		return err
	}
	defer s.lock(r.InstanceID)()
	in, ir, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return err
	}
	if s.state(r.InstanceID) != StateRunning {
		return notReady{fmt.Sprintf("%s isn't running, so the database can't be dropped from it; start it first", in.Name)}
	}
	root, err := s.sealer.open(ir.RootPassword)
	if err != nil {
		return err
	}
	if r.RoUsername != "" {
		if err := dropReadOnly(ctx, in, s.dir(r.InstanceID), root, r.DbName, r.RoUsername); err != nil {
			return fmt.Errorf("dropping its read-only user on %s: %w", in.Name, err)
		}
	}
	if r.ViewerUsername != "" && isSQL(in.Engine) {
		if err := dropReadOnly(ctx, in, s.dir(r.InstanceID), root, r.DbName, r.ViewerUsername); err != nil {
			return fmt.Errorf("dropping its reader on %s: %w", in.Name, err)
		}
	}
	if err := dropDatabase(ctx, in, s.dir(r.InstanceID), root, r.DbName, r.Username); err != nil {
		return fmt.Errorf("dropping it on %s: %w", in.Name, err)
	}
	if err := s.o.Store.Write.DeleteDatabase(ctx, r.ID); err != nil {
		return err
	}
	if r.RoUsername != "" && in.Engine == Postgres {
		return s.reloadHBA(ctx, in, s.dir(r.InstanceID), root)
	}
	return nil
}

// Password returns a database's password.
func (s *Service) Password(ctx context.Context, serverID, id string) (string, error) {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return "", err
	}
	return s.sealer.open(r.Password)
}

// PasswordByID returns any database's password, for admins.
func (s *Service) PasswordByID(ctx context.Context, id string) (string, error) {
	r, err := s.byID(ctx, id)
	if err != nil {
		return "", err
	}
	return s.sealer.open(r.Password)
}

// ResetPassword gives a database's user a new password and returns it.
// The servers using it get it at their next start.
func (s *Service) ResetPassword(ctx context.Context, serverID, id string) (string, error) {
	r, err := s.database(ctx, serverID, id)
	if err != nil {
		return "", err
	}
	return s.resetPassword(ctx, r)
}

// ResetPasswordByID is ResetPassword for any database, for admins.
func (s *Service) ResetPasswordByID(ctx context.Context, id string) (string, error) {
	r, err := s.byID(ctx, id)
	if err != nil {
		return "", err
	}
	return s.resetPassword(ctx, r)
}

func (s *Service) resetPassword(ctx context.Context, r store.Database) (string, error) {
	if err := s.busy(r.InstanceID); err != nil {
		return "", err
	}
	defer s.lock(r.InstanceID)()
	in, ir, err := s.instance(ctx, s.o.Store.Read, r.InstanceID)
	if err != nil {
		return "", err
	}
	if s.state(r.InstanceID) != StateRunning {
		return "", notReady{fmt.Sprintf("%s isn't running; start it first", in.Name)}
	}
	root, err := s.sealer.open(ir.RootPassword)
	if err != nil {
		return "", err
	}
	pass, err := password()
	if err != nil {
		return "", err
	}
	sealed, err := s.sealer.seal(pass)
	if err != nil {
		return "", err
	}
	if err := setPassword(ctx, in, s.dir(r.InstanceID), root, r.Username, pass); err != nil {
		return "", err
	}
	return pass, s.o.Store.Write.SetDatabasePassword(ctx, store.SetDatabasePasswordParams{Password: sealed, ID: r.ID})
}

// Env is the environment variables a server gets for its databases
// (docs/DATABASES.md#using-a-database-from-a-server): for its first SQL
// database DB_HOST and the rest with DATABASE_URL, for its first Redis
// one REDIS_HOST and the rest with REDIS_URL, for its first MongoDB one
// MONGODB_HOST and the rest with MONGODB_URI; and with more than one
// database, each one's again prefixed with its name.
func (s *Service) Env(ctx context.Context, serverID string) ([]string, error) {
	rows, err := s.o.Store.Read.ListServerDatabases(ctx, serverID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	insts, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	first := map[string]bool{} // families given unprefixed variables
	for _, r := range rows {
		pass, err := s.sealer.open(r.Password)
		if err != nil {
			return nil, err
		}
		d := toDatabase(r, insts, nil)
		family, vars := envVars(d, pass)
		prefix := strings.ToUpper(r.Label) + "_"
		for _, v := range vars {
			if !first[family] {
				out = append(out, v[0]+"="+v[1])
			}
			if len(rows) > 1 {
				out = append(out, prefix+v[0]+"="+v[1])
			}
		}
		first[family] = true
	}
	return out, nil
}

// envVars are one database's variables, and the family they're for.
func envVars(d Database, pass string) (string, [][2]string) {
	port := fmt.Sprint(d.Port)
	switch d.Engine {
	case Redis, Valkey:
		return Redis, [][2]string{
			{"REDIS_HOST", d.Host},
			{"REDIS_PORT", port},
			{"REDIS_USER", d.User},
			{"REDIS_PASSWORD", pass},
			{"REDIS_URL", fmt.Sprintf("redis://%s:%s@%s:%d/0", d.User, pass, d.Host, d.Port)},
		}
	case MongoDB:
		return MongoDB, [][2]string{
			{"MONGODB_HOST", d.Host},
			{"MONGODB_PORT", port},
			{"MONGODB_DATABASE", d.Name},
			{"MONGODB_USER", d.User},
			{"MONGODB_PASSWORD", pass},
			{"MONGODB_URI", fmt.Sprintf("mongodb://%s:%s@%s:%d/%s?authSource=%s", d.User, pass, d.Host, d.Port, d.Name, d.Name)},
		}
	}
	scheme := "mysql"
	if d.Engine == Postgres {
		scheme = "postgres"
	}
	return "sql", [][2]string{
		{"DB_HOST", d.Host},
		{"DB_PORT", port},
		{"DB_NAME", d.Name},
		{"DB_USER", d.User},
		{"DB_PASSWORD", pass},
		{"DATABASE_URL", fmt.Sprintf("%s://%s:%s@%s:%d/%s", scheme, d.User, pass, d.Host, d.Port, d.Name)},
	}
}

// ServerDeleted removes a deleted server's links. Its databases stay on
// their instances, for the servers still using them or to link again
// (DECISIONS #342).
func (s *Service) ServerDeleted(ctx context.Context, serverID string) {
	if err := s.o.Store.Write.DeleteServerLinks(ctx, serverID); err != nil {
		s.o.Log.Error("removing a deleted server's database links failed; they go at the next start", "server", serverID, "err", err)
	}
	s.changed(ctx)
}

// pruneLinks removes the links of servers that are gone (one deleted
// while its links couldn't be removed).
func (s *Service) pruneLinks(ctx context.Context) {
	ids, err := s.o.Store.Read.ListServerIDs(ctx)
	if err != nil {
		return
	}
	links, err := s.o.Store.Read.ListDatabaseLinks(ctx)
	if err != nil {
		return
	}
	for _, l := range links {
		if slices.Contains(ids, l.ServerID) {
			continue
		}
		if _, err := s.o.Store.Write.DeleteDatabaseLink(ctx, store.DeleteDatabaseLinkParams{DatabaseID: l.DatabaseID, ServerID: l.ServerID}); err != nil {
			s.o.Log.Warn("removing a deleted server's database link failed", "server", l.ServerID, "err", err)
		}
	}
}

// Guards are the firewall's rules keeping instances on the owner's
// networks to the servers on those networks (DECISIONS #342): any of them
// reaches the instance, and each database's own user keeps them apart.
func (s *Service) Guards(ctx context.Context) []firewall.Guard {
	q := s.o.Store.Read
	iaddrs, err := q.ListInstanceAddresses(ctx)
	if err != nil {
		s.o.Log.Error("reading instances' addresses failed", "err", err)
		return nil
	}
	saddrs, err := q.ListServerAddresses(ctx)
	if err != nil {
		return nil
	}
	insts, err := q.ListInstances(ctx)
	if err != nil {
		return nil
	}
	onNet := map[string][]string{} // instance ID -> its networks now
	ext := map[string]bool{}       // instance ID -> it has external access
	for _, r := range insts {
		var ids []string
		_ = json.Unmarshal([]byte(r.Networks), &ids)
		onNet[r.ID] = ids
		ext[r.ID] = r.ExternalPort != 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []firewall.Guard
	for _, ia := range iaddrs {
		if !slices.Contains(onNet[ia.InstanceID], ia.NetworkID) {
			continue
		}
		addr, err := netip.ParseAddr(ia.Ip)
		if err != nil {
			continue
		}
		g := firewall.Guard{
			Bridge: networks.Bridge(ia.NetworkID), Addr: addr, IPv6: s.ipv6[ia.InstanceID][networks.DockerName(ia.NetworkID)],
			External: ext[ia.InstanceID],
		}
		for _, sa := range saddrs {
			if sa.NetworkID == ia.NetworkID {
				if a, err := netip.ParseAddr(sa.Ip); err == nil && !slices.Contains(g.Allowed, a) {
					g.Allowed = append(g.Allowed, a)
				}
			}
		}
		out = append(out, g)
	}
	return out
}

func (s *Service) changed(ctx context.Context) {
	if s.o.Changed == nil {
		return
	}
	if err := s.o.Changed(ctx, s.Guards(ctx), s.Externals(ctx)); err != nil {
		s.o.Log.Error("reloading the firewall after a database change failed", "err", err)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // plain structs and slices
	}
	return string(b)
}

// must is a list, or none when it couldn't be read: for checks that the
// next step repeats.
func must[T any](list []T, _ error) []T { return list }

// lastLog is the end of an instance's own log, for an error saying why it
// didn't start: the lines that look like errors, or else the last ones.
func (s *Service) lastLog(ctx context.Context, id string) string {
	out, err := s.o.Runtime.ServiceLogs(context.WithoutCancel(ctx), ContainerName(id), 200)
	if err != nil || strings.TrimSpace(out) == "" {
		return fmt.Sprintf("; its log on the node has why (docker logs %s)", ContainerName(id))
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var picked []string
	for _, l := range lines {
		ll := strings.ToLower(l)
		if strings.Contains(ll, "error") || strings.Contains(ll, "fatal") || strings.Contains(ll, `"s":"e"`) || strings.Contains(ll, `"s":"f"`) {
			picked = append(picked, l)
		}
	}
	if len(picked) == 0 {
		picked = lines
	}
	if len(picked) > 3 {
		picked = picked[len(picked)-3:]
	}
	for i, l := range picked {
		if len(l) > 300 {
			picked[i] = l[:300] + "…"
		}
	}
	return "; its log ends: " + strings.Join(picked, " | ")
}

func (s *Service) kernel() string {
	if s.o.Kernel == nil {
		return ""
	}
	return s.o.Kernel()
}

// Versions are the versions offered, with what this node can't run
// marked (Version.Unavailable).
func (s *Service) Versions() []Version { return offered(s.kernel()) }
