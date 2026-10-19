package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// External access (docs/DATABASES.md#external-access): an instance can get
// a port on the node, for a desktop tool or an app elsewhere. Three things
// keep it from being a way in:
//
//   - The firewall drops connections to the port from any address not on
//     its allowlist, before Docker forwards them, the node's own
//     containers included (firewall.External).
//   - The instance serves TLS with a certificate Wings makes (tls.go), and
//     requires it from every address but its own networks': servers there
//     connect as before, since many plugins don't do TLS. PostgreSQL does
//     it in pg_hba.conf; MariaDB and MySQL with an account per network for
//     each user, besides the one for everywhere else that requires TLS
//     (syncAccounts). Docker keeps the client's own address (userland-proxy
//     off); with it on, clients arrive from the network's gateway, which is
//     treated as outside.
//   - The users are the databases' own, which reach only their database.

// External is an instance's external access, as reported.
type External struct {
	Port  int      `json:"port"`
	Allow []string `json:"allow"`
	// Host is the node's name in DNS, which the certificate names.
	Host string `json:"host,omitempty"`
	// CA is the certificate clients check the instance's against (PEM).
	CA string `json:"ca,omitempty"`
}

// ExternalInput is what node.db.instance.external takes: Port 0 turns it
// off.
type ExternalInput struct {
	ID    string   `json:"id"`
	Port  int      `json:"port"`
	Allow []string `json:"allow"`
}

// MaxAllow bounds an allowlist.
const MaxAllow = 32

// parseAllow checks an allowlist: addresses and ranges, at least one.
func parseAllow(list []string) ([]netip.Prefix, error) {
	if len(list) == 0 {
		return nil, invalid{"list at least one address or range that may connect, like 203.0.113.7 or 198.51.100.0/24"}
	}
	if len(list) > MaxAllow {
		return nil, invalid{fmt.Sprintf("an allowlist has at most %d entries", MaxAllow)}
	}
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		var p netip.Prefix
		if a, err := netip.ParseAddr(s); err == nil {
			if a.Zone() != "" {
				return nil, invalid{fmt.Sprintf("%q has a zone; list the address without it", s)}
			}
			p = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
		} else if p, err = netip.ParsePrefix(s); err == nil {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()).Masked()
		} else {
			return nil, invalid{fmt.Sprintf("%q isn't an address or a range like 198.51.100.0/24", s)}
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if p.IsSingleIP() {
			out = append(out, p.Addr().String())
		} else {
			out = append(out, p.String())
		}
	}
	return out
}

// external reads an instance row's external access: port 0 is off.
func external(r store.DatabaseInstance) (int, []netip.Prefix) {
	if r.ExternalPort == 0 {
		return 0, nil
	}
	var list []string
	_ = json.Unmarshal([]byte(r.ExternalAllow), &list)
	allow, _ := parseAllow(list)
	return int(r.ExternalPort), allow
}

// SetExternal turns an instance's external access on, changes it, or
// turns it off (Port 0), and restarts it if it's running.
func (s *Service) SetExternal(ctx context.Context, in ExternalInput) (Instance, error) {
	if err := s.busy(in.ID); err != nil {
		return Instance{}, err
	}
	unlock := s.lock(in.ID)
	cur, r, err := s.instance(ctx, s.o.Store.Read, in.ID)
	if err != nil {
		unlock()
		return Instance{}, err
	}
	allow := "[]"
	if in.Port != 0 {
		list, err := parseAllow(in.Allow)
		if err == nil {
			err = s.checkPort(ctx, in.ID, in.Port, int(r.ExternalPort))
		}
		if err != nil {
			unlock()
			return Instance{}, err
		}
		allow = mustJSON(prefixStrings(list))
	}
	err = s.o.Store.Write.SetInstanceExternal(ctx, store.SetInstanceExternalParams{
		ExternalPort: int64(in.Port), ExternalAllow: allow, UpdatedAt: time.Now().Unix(), ID: in.ID,
	})
	unlock()
	if err != nil {
		return Instance{}, err
	}
	s.changed(ctx)
	if cur.DesiredState == StateRunning {
		s.setStatus(in.ID, StateStarting, nil)
		go s.ensure(context.WithoutCancel(ctx), in.ID)
	}
	return s.Get(ctx, in.ID)
}

// checkPort checks a port can be an instance's: not Wings' own, not a
// server's, not another instance's, and free on the node.
func (s *Service) checkPort(ctx context.Context, id string, port, current int) error {
	if port < 1024 || port > 65535 {
		return invalid{"the port is between 1024 and 65535"}
	}
	if slices.Contains(s.o.ReservedPorts, port) {
		return invalid{fmt.Sprintf("port %d is one of Raptor's own on this node", port)}
	}
	allocs, err := s.o.Store.Read.ListAllocations(ctx)
	if err != nil {
		return err
	}
	for _, a := range allocs {
		if int(a.Port) == port {
			name := a.ServerID
			if sv, err := s.o.Store.Read.GetServer(ctx, a.ServerID); err == nil {
				name = sv.Name
			}
			return invalid{fmt.Sprintf("port %d is allocated to the server %s", port, name)}
		}
	}
	others, err := s.o.Store.Read.ListInstanceExternalPorts(ctx)
	if err != nil {
		return err
	}
	for _, o := range others {
		if o.ID != id && int(o.ExternalPort) == port {
			return invalid{fmt.Sprintf("port %d is %s's", port, o.Name)}
		}
	}
	if port != current && s.o.CheckPort != nil {
		if err := s.o.CheckPort(port); err != nil {
			return invalid{fmt.Sprintf("something on the node already uses port %d", port)}
		}
	}
	return nil
}

// internal are the networks an instance's servers reach it from: the
// owner's it's on, or the server network.
func (s *Service) internal(ctx context.Context, in Instance) ([]containers.Network, error) {
	if len(in.Networks) == 0 {
		if s.o.ServerNetwork.Subnet.IsValid() {
			return []containers.Network{s.o.ServerNetwork}, nil
		}
		return nil, nil
	}
	return s.o.Networks.Resolve(ctx, in.Networks)
}

// tlsNames are the names an instance's certificate has: the node's DNS
// name first, if it has one, then the instance's host names.
func (s *Service) tlsNames(in Instance) []string {
	var out []string
	if s.o.Hostname != "" {
		out = append(out, s.o.Hostname)
	}
	for _, a := range aliases(in.ID, in.Name) {
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// externalSpec adds external access to an instance's container: the
// port, its certificate, and TLS turned on.
func externalSpec(spec *containers.ServiceSpec, in Instance, dir string, port int) {
	spec.Publish = []containers.Published{{HostPort: port, Port: in.Port}}
	if in.Engine == Postgres {
		spec.Cmd = append(spec.Cmd, "-c", "ssl=on", "-c", "ssl_cert_file="+tlsCert, "-c", "ssl_key_file="+tlsKey)
		return
	}
	spec.Mounts = append(spec.Mounts, containers.Mount{Source: confDir(dir), Target: "/etc/raptor", ReadOnly: true})
	spec.Cmd = append(spec.Cmd, "--ssl-ca="+tlsCA, "--ssl-cert="+tlsCert, "--ssl-key="+tlsKey)
}

// account is a MariaDB or MySQL user and what it's granted.
type account struct {
	user, pass, grant, database string
}

// mysqlHost is a network as a MySQL account host: address/netmask, which
// MariaDB and MySQL both take, and which ranks above '%'.
func mysqlHost(p netip.Prefix) string {
	var b [4]byte
	for i := range p.Bits() {
		b[i/8] |= 0x80 >> (i % 8)
	}
	return p.Masked().Addr().String() + "/" + netip.AddrFrom4(b).String()
}

// accountHosts are the hosts each MariaDB or MySQL user has accounts for,
// and whether each requires TLS: with external access, '%' does, the
// socket (localhost) and the instance's networks don't, and their gateways
// do (connections Docker's userland proxy forwards from outside come from
// there). Without it, '%' alone, requiring nothing.
func accountHosts(on bool, nets []containers.Network) map[string]bool {
	if !on {
		return map[string]bool{"%": false}
	}
	out := map[string]bool{"%": true, "localhost": false}
	for _, n := range nets {
		if n.Subnet.IsValid() && n.Subnet.Addr().Is4() {
			out[mysqlHost(n.Subnet)] = false
		}
		if n.Gateway.IsValid() && n.Gateway.Is4() {
			out[n.Gateway.String()] = true
		}
	}
	return out
}

// syncAccounts gives every user on a MariaDB or MySQL instance exactly
// the accounts accountHosts says, with its password and rights.
func (s *Service) syncAccounts(ctx context.Context, in Instance, root string, on bool, nets []containers.Network) error {
	if in.Engine == Postgres {
		return nil
	}
	dbs, err := s.instanceDatabases(ctx, in.ID)
	if err != nil {
		return err
	}
	var accts []account
	for _, d := range dbs {
		pass, err := s.sealer.open(d.Password)
		if err != nil {
			return err
		}
		accts = append(accts, account{d.Username, pass, "ALL PRIVILEGES", d.DbName})
		if d.RoUsername != "" && d.RoPassword != nil {
			ro, err := s.sealer.open(d.RoPassword)
			if err != nil {
				return err
			}
			accts = append(accts, account{d.RoUsername, ro, "SELECT, SHOW VIEW", d.DbName})
		}
	}
	db, err := connect(in, s.dir(in.ID), root)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	want := accountHosts(on, nets)
	for _, a := range accts {
		if err := checkNames(a.user, a.database); err != nil {
			return err
		}
		if err := checkPassword(a.pass); err != nil {
			return err
		}
		have, err := userHosts(ctx, db, a.user)
		if err != nil {
			return err
		}
		for _, h := range have {
			if _, ok := want[h]; !ok {
				if err := exec(ctx, db, "DROP USER IF EXISTS '"+a.user+"'@'"+h+"'"); err != nil {
					return err
				}
			}
		}
		for h, ssl := range want {
			req := "NONE"
			if ssl {
				req = "SSL"
			}
			if err := exec(ctx, db,
				"CREATE USER IF NOT EXISTS '"+a.user+"'@'"+h+"' IDENTIFIED BY '"+a.pass+"'",
				"ALTER USER '"+a.user+"'@'"+h+"' IDENTIFIED BY '"+a.pass+"' REQUIRE "+req,
				"GRANT "+a.grant+" ON `"+a.database+"`.* TO '"+a.user+"'@'"+h+"'",
			); err != nil {
				return fmt.Errorf("%s@%s: %w", a.user, h, err)
			}
		}
	}
	return nil
}

// userHosts are the hosts a MariaDB or MySQL user has accounts for.
func userHosts(ctx context.Context, db *sql.DB, user string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT Host FROM mysql.user WHERE User = ?", user)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// reconcile brings a MariaDB or MySQL instance's accounts in line after a
// user was made, changed, or given a new password.
func (s *Service) reconcile(ctx context.Context, in Instance, r store.DatabaseInstance, root string) error {
	if in.Engine == Postgres {
		return nil
	}
	port, _ := external(r)
	nets, err := s.internal(ctx, in)
	if err != nil {
		return err
	}
	return s.syncAccounts(ctx, in, root, port != 0, nets)
}

// Externals are the firewall's rules for instances' external ports.
func (s *Service) Externals(ctx context.Context) []firewall.External {
	rows, err := s.o.Store.Read.ListInstances(ctx)
	if err != nil {
		return nil
	}
	var out []firewall.External
	for _, r := range rows {
		if port, allow := external(r); port != 0 {
			out = append(out, firewall.External{Port: port, Allow: allow})
		}
	}
	return out
}

// reportExternal fills in an instance's external access for List.
func (s *Service) reportExternal(in *Instance, r store.DatabaseInstance) {
	port, allow := external(r)
	if port == 0 {
		return
	}
	ext := &External{Port: port, Allow: prefixStrings(allow), Host: s.o.Hostname}
	if b, err := os.ReadFile(caPath(s.dir(in.ID))); err == nil {
		ext.CA = string(b)
	}
	in.External = ext
}
