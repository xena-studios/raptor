package containers

import (
	"errors"
	"net"
	"strconv"
	"testing"
)

func TestCheckPorts(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	taken := l.Addr().(*net.TCPAddr).Port

	if err := CheckPorts([]Port{{IP: "127.0.0.1", Port: taken}}); !errors.Is(err, ErrPortInUse) {
		t.Fatalf("taken port %d: got %v, want ErrPortInUse", taken, err)
	}

	// Find a free port by binding and releasing it.
	l2, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // test listener
	if err != nil {
		t.Fatal(err)
	}
	free := l2.Addr().(*net.TCPAddr).Port
	_ = l2.Close()
	if err := CheckPorts([]Port{{IP: "127.0.0.1", Port: free}}); err != nil {
		t.Fatalf("free port %s: %v", strconv.Itoa(free), err)
	}
}
