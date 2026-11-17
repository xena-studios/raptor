package rcon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Source RCON packet types (developer.valvesoftware.com/wiki/Source_RCON_Protocol).
const (
	typeAuth         = 3
	typeAuthResponse = 2
	typeExec         = 2
	typeResponse     = 0
)

// idle is how long to wait for more of an answer after a packet: big
// answers come in several packets, and not every game (ARK, Palworld)
// mirrors the empty packet that would mark the end.
const idle = 250 * time.Millisecond

type sourceConn struct {
	c  net.Conn
	id int32
}

func dialSource(ctx context.Context, addr, password string) (*sourceConn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &sourceConn{c: c}
	if err := s.auth(ctx, password); err != nil {
		_ = c.Close()
		return nil, err
	}
	return s, nil
}

func (s *sourceConn) Close() error { return s.c.Close() }

func (s *sourceConn) auth(ctx context.Context, password string) error {
	if dl, ok := ctx.Deadline(); ok {
		_ = s.c.SetDeadline(dl)
	}
	id := s.next()
	if err := writePacket(s.c, id, typeAuth, password); err != nil {
		return err
	}
	// Source games send an empty response first, then the auth answer:
	// its ID is the request's, or -1 for a wrong password.
	for range 4 {
		pid, typ, _, err := readPacket(s.c)
		if err != nil {
			return fmt.Errorf("rcon login: %w", err)
		}
		if pid == -1 {
			return ErrAuth
		}
		if typ == typeAuthResponse && pid == id {
			return nil
		}
	}
	return errors.New("rcon login: no answer")
}

func (s *sourceConn) next() int32 {
	s.id++
	return s.id
}

func (s *sourceConn) Exec(ctx context.Context, cmd string) (string, error) {
	if strings.ContainsAny(cmd, "\x00\n\r") {
		return "", errors.New("rcon: a command is one line")
	}
	dl := time.Now().Add(Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(dl) {
		dl = d
	}
	_ = s.c.SetDeadline(dl)
	id := s.next()
	if err := writePacket(s.c, id, typeExec, cmd); err != nil {
		return "", err
	}
	var out strings.Builder
	first := true
	for {
		pid, typ, body, err := readPacket(s.c)
		var ne net.Error
		switch {
		case err != nil && !first && errors.As(err, &ne) && ne.Timeout():
			return out.String(), nil
		case err != nil:
			return "", fmt.Errorf("rcon: %w", err)
		}
		if typ == typeResponse && (pid == id || pid == 0) {
			out.WriteString(body)
			if out.Len() > maxReply {
				return "", errors.New("rcon: the answer is too long")
			}
			if first {
				first = false
			}
		}
		_ = s.c.SetReadDeadline(time.Now().Add(idle))
	}
}

func writePacket(w io.Writer, id, typ int32, body string) error {
	if len(body) > 4086 {
		return errors.New("rcon: the command is too long")
	}
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, int32(len(body)+10)) //nolint:gosec // bounded above
	_ = binary.Write(&b, binary.LittleEndian, id)
	_ = binary.Write(&b, binary.LittleEndian, typ)
	b.WriteString(body)
	b.Write([]byte{0, 0})
	_, err := w.Write(b.Bytes())
	return err
}

func readPacket(r io.Reader) (id, typ int32, body string, err error) {
	var size int32
	if err := binary.Read(r, binary.LittleEndian, &size); err != nil {
		return 0, 0, "", err
	}
	if size < 8 || size > maxReply {
		return 0, 0, "", fmt.Errorf("bad packet size %d", size)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, 0, "", err
	}
	id = int32(binary.LittleEndian.Uint32(buf[0:4]))  //nolint:gosec // the wire's int32
	typ = int32(binary.LittleEndian.Uint32(buf[4:8])) //nolint:gosec // the wire's int32
	// The body ends in two NULs; some games (Palworld) leave them out.
	return id, typ, string(bytes.TrimRight(buf[8:], "\x00")), nil
}
