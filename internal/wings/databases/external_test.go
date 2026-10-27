package databases

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/netip"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

func TestParseAllow(t *testing.T) {
	got, err := parseAllow([]string{" 203.0.113.7 ", "198.51.100.9/24", "2001:db8::1", "203.0.113.7", "::ffff:192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"203.0.113.7", "198.51.100.0/24", "2001:db8::1", "192.0.2.1"}
	if s := prefixStrings(got); !slices.Equal(s, want) {
		t.Errorf("got %v, want %v", s, want)
	}
	for _, bad := range [][]string{nil, {"example.com"}, {"10.0.0.0/33"}, {"fe80::1%eth0"}} {
		if _, err := parseAllow(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

var internalNets = []containers.Network{{
	Subnet: netip.MustParsePrefix("172.27.0.0/16"), Gateway: netip.MustParseAddr("172.27.0.1"),
}}

func TestAccountHosts(t *testing.T) {
	if got := accountHosts(false, internalNets); len(got) != 1 || got["%"] {
		t.Errorf("without external access: %v", got)
	}
	got := accountHosts(true, internalNets)
	want := map[string]bool{"%": true, "localhost": false, "172.27.0.0/255.255.0.0": false, "172.27.0.1": true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for h, ssl := range want {
		if v, ok := got[h]; !ok || v != ssl {
			t.Errorf("%s: %v %v", h, v, ok)
		}
	}
	if h := mysqlHost(netip.MustParsePrefix("10.20.30.0/23")); h != "10.20.30.0/255.255.254.0" {
		t.Errorf("mysqlHost %q", h)
	}
}

func TestPgHBAExternal(t *testing.T) {
	ro := [][2]string{{"s1_a", "r1_a"}}
	off := pgHBA(ro, false, nil)
	if !strings.Contains(off, "host sameuser all all scram-sha-256\n") || strings.Contains(off, "hostssl") {
		t.Errorf("without external access:\n%s", off)
	}
	on := pgHBA(ro, true, internalNets)
	lines := strings.Split(strings.TrimSpace(on), "\n")
	want := []string{
		"local all all scram-sha-256",
		"host all postgres all reject",
		"hostnossl all all 172.27.0.1/32 reject",
		"host sameuser all 172.27.0.0/16 scram-sha-256",
		"host s1_a r1_a 172.27.0.0/16 scram-sha-256",
		"hostssl sameuser all all scram-sha-256",
		"hostssl s1_a r1_a all scram-sha-256",
	}
	if !slices.Equal(lines[1:], want) {
		t.Errorf("with external access:\n%s", on)
	}
}

func TestTLS(t *testing.T) {
	dir := t.TempDir()
	names := []string{"node.raptornodes.net", "db.db.raptor"}
	made, err := ensureTLS(dir, "DB", names)
	if err != nil || !made {
		t.Fatalf("first: %v %v", made, err)
	}
	if made, err := ensureTLS(dir, "DB", names); err != nil || made {
		t.Errorf("again, with nothing changed: %v %v", made, err)
	}
	caPEM, _ := os.ReadFile(caPath(dir))
	certPEM, _ := os.ReadFile(certPath(dir))
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	p, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(p.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: "node.raptornodes.net"}); err != nil {
		t.Errorf("the certificate doesn't verify against the authority: %v", err)
	}
	if fi, err := os.Stat(keyPath(dir)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode: %v", err)
	}
	// A new name makes a new certificate from the same authority.
	if made, err := ensureTLS(dir, "DB", append(names, "other.db.raptor")); err != nil || !made {
		t.Errorf("new name: %v %v", made, err)
	}
	if again, _ := os.ReadFile(caPath(dir)); string(again) != string(caPEM) {
		t.Error("the authority changed")
	}
}

func TestServiceSpecHashUnchanged(t *testing.T) {
	// Instances without external access keep the spec they had, so a
	// Wings update doesn't restart them.
	s := serviceSpec(Instance{ID: "x", Engine: MariaDB, Limits: Limits{MemoryMiB: 512}}, "/d", "pw", "")
	if s.Publish != nil {
		t.Error("published a port without external access")
	}
	externalSpec(&s, Instance{ID: "x", Engine: MariaDB, Port: 3306}, "/d", 33060)
	if len(s.Publish) != 1 || s.Publish[0] != (containers.Published{HostPort: 33060, Port: 3306}) {
		t.Errorf("publish %v", s.Publish)
	}
	if !slices.Contains(s.Cmd, "--ssl-cert="+tlsCert) || s.Mounts[len(s.Mounts)-1].Target != "/etc/raptor" {
		t.Errorf("tls: %v %v", s.Cmd, s.Mounts)
	}
}

// Redis and Valkey publish a second port that speaks only TLS; their
// plain one isn't published. MongoDB has no external access.
func TestRedisExternalSpec(t *testing.T) {
	for _, engine := range []string{Redis, Valkey} {
		in := Instance{ID: "x", Engine: engine, Port: 6379, Limits: Limits{MemoryMiB: 256}}
		s := serviceSpec(in, "/d", "pw", "")
		if want := map[string]string{Redis: "redis-server", Valkey: "valkey-server"}[engine]; s.Cmd[0] != want {
			t.Errorf("%s runs %s", engine, s.Cmd[0])
		}
		externalSpec(&s, in, "/d", 16379)
		if len(s.Publish) != 1 || s.Publish[0] != (containers.Published{HostPort: 16379, Port: redisTLSPort}) {
			t.Errorf("%s publishes %v", engine, s.Publish)
		}
		cmd := strings.Join(s.Cmd, " ")
		for _, want := range []string{"--tls-port 6380", "--tls-cert-file " + tlsCert, "--tls-key-file " + tlsKey, "--tls-auth-clients no", "--unixsocket /run/redis/redis.sock"} {
			if !strings.Contains(cmd, want) {
				t.Errorf("%s: %q missing from %s", engine, want, cmd)
			}
		}
		if strings.Contains(cmd, "--port ") || s.Mounts[len(s.Mounts)-1] != (containers.Mount{Source: "/d/conf", Target: "/etc/raptor", ReadOnly: true}) {
			t.Errorf("%s: %s %v", engine, cmd, s.Mounts)
		}
	}
	if hasExternal(MongoDB) || !hasExternal(Valkey) || !hasExternal(Postgres) {
		t.Error("hasExternal")
	}
}
