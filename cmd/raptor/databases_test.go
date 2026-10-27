package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

func TestPrintInstances(t *testing.T) {
	var b bytes.Buffer
	if err := printInstances(&b, nil); err != nil || !strings.Contains(b.String(), "no database instances") {
		t.Errorf("none: %q %v", b.String(), err)
	}
	b.Reset()
	err := printInstances(&b, []*localv1.DatabaseInstanceInfo{
		{
			Id: "0199e2e0-0000-7000-8000-0000000000d1", Name: "main", Engine: "postgres", Version: "17", State: "running",
			SampledAt: timestamppb.New(time.Now()), MemoryBytes: 300 << 20, MemoryLimitBytes: 1 << 30,
			DiskBytes: 2 << 30, DiskLimitBytes: 10 << 30, Connections: 12, MaxConnections: 100, Databases: 3,
			Networks: []*localv1.InstanceNetwork{{Id: "n", Name: "lan", Address: "10.64.0.2"}},
		},
		{Id: "0199e2e0-0000-7000-8000-0000000000d2", Name: "cache", Engine: "redis", Version: "8.2", State: "failed", Error: "it keeps stopping (exit code 1)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("%q", b.String())
	}
	for _, want := range []string{"0000000000d1", "main", "PostgreSQL 17", "running", "300.0 MiB / 1.0 GiB", "2.0 GiB / 10.0 GiB", "12 / 100", "3", "lan (10.64.0.2)"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("running instance: no %q in %q", want, lines[1])
		}
	}
	for _, want := range []string{"Redis 8.2", "failed (it keeps stopping (exit code 1))", "server network"} {
		if !strings.Contains(lines[2], want) {
			t.Errorf("failed instance: no %q in %q", want, lines[2])
		}
	}
}
