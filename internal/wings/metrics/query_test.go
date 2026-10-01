package metrics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// fakeMinecraft answers one Server List Ping like a Java edition server.
func fakeMinecraft(t *testing.T, status string) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		r := bufio.NewReader(c)
		// Handshake, then the status request.
		for range 2 {
			n, err := readVarint(r)
			if err != nil {
				return
			}
			if _, err := io.CopyN(io.Discard, r, int64(n)); err != nil {
				return
			}
		}
		var body bytes.Buffer
		body.WriteByte(0x00)
		body.Write(varint(int32(len(status))))
		body.WriteString(status)
		_, _ = c.Write(append(varint(int32(body.Len())), body.Bytes()...))
	}()
	return l.Addr().String()
}

// fakeSource answers A2S_INFO, asking for a challenge first.
func fakeSource(t *testing.T, players, maxPlayers byte) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1400)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			req := buf[:n]
			if len(req) == len(a2sInfo) { // no challenge yet
				_, _ = pc.WriteTo([]byte{0xff, 0xff, 0xff, 0xff, 'A', 1, 2, 3, 4}, addr)
				continue
			}
			if !bytes.Equal(req[len(req)-4:], []byte{1, 2, 3, 4}) {
				continue
			}
			var b bytes.Buffer
			b.Write([]byte{0xff, 0xff, 0xff, 0xff, 'I', 17})
			for _, s := range []string{"My Server", "de_dust2", "csgo", "Counter-Strike"} {
				b.WriteString(s)
				b.WriteByte(0)
			}
			_ = binary.Write(&b, binary.LittleEndian, uint16(730))
			b.Write([]byte{players, maxPlayers, 0})
			_, _ = pc.WriteTo(b.Bytes(), addr)
		}
	}()
	return pc.LocalAddr().String()
}

func TestQueryMinecraft(t *testing.T) {
	addr := fakeMinecraft(t, `{"version":{"name":"1.21.10","protocol":773},"players":{"max":20,"online":3},"description":"hi"}`)
	p, err := QueryPlayers(context.Background(), "minecraft", addr)
	if err != nil || p != (Players{Online: 3, Max: 20}) {
		t.Fatalf("players %+v, %v", p, err)
	}
}

func TestQuerySource(t *testing.T) {
	p, err := QueryPlayers(context.Background(), "source", fakeSource(t, 7, 24))
	if err != nil || p != (Players{Online: 7, Max: 24}) {
		t.Fatalf("players %+v, %v", p, err)
	}
}

func TestQueryFailures(t *testing.T) {
	ctx := context.Background()
	if _, err := QueryPlayers(ctx, "minecraft", "127.0.0.1:1"); err == nil {
		t.Error("nothing listening: no error")
	}
	if _, err := QueryPlayers(ctx, "minecraft", fakeMinecraft(t, `not json`)); err == nil {
		t.Error("garbage: no error")
	}
	// A UDP port nobody answers on times out instead of hanging.
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	defer func() { _ = pc.Close() }()
	if _, err := QueryPlayers(ctx, "source", pc.LocalAddr().String()); err == nil {
		t.Error("silent server: no error")
	}
	if _, err := QueryPlayers(ctx, "quake", "127.0.0.1:1"); err == nil {
		t.Error("unknown protocol: no error")
	}
}

func FuzzParseA2SInfo(f *testing.F) {
	f.Add([]byte{17, 'a', 0, 'b', 0, 'c', 0, 'd', 0, 0xda, 0x02, 3, 10, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseA2SInfo(b)
	})
}

func TestVarint(t *testing.T) {
	for _, v := range []int32{0, 1, 127, 128, 300, 2097151, -1} {
		got, err := readVarint(bytes.NewReader(varint(v)))
		if err != nil || got != v {
			t.Errorf("varint %d: %d, %v", v, got, err)
		}
	}
}
