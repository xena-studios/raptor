package metrics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Players is a game's answer to a player query.
type Players struct {
	Online, Max int
}

// queryTimeout bounds each query: a game that doesn't answer quickly is
// skipped until the next sample.
const queryTimeout = 2 * time.Second

// QueryPlayers asks a game server for its player count with the protocol its
// egg names.
func QueryPlayers(ctx context.Context, protocol, addr string) (Players, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	switch protocol {
	case "minecraft":
		return queryMinecraft(ctx, addr)
	case "source":
		return querySource(ctx, addr)
	}
	return Players{}, fmt.Errorf("unknown player query %q", protocol)
}

// queryMinecraft uses the Java edition's Server List Ping: a handshake into
// the status state, a status request, and a JSON reply.
func queryMinecraft(ctx context.Context, addr string) (Players, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return Players{}, err
	}
	defer func() { _ = c.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	host, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)

	var hs bytes.Buffer
	hs.WriteByte(0x00)                                    // handshake
	hs.Write(varint(-1))                                  // protocol version: any, for status
	hs.Write(varint(int32(len(host))))                    //nolint:gosec // a hostname
	hs.WriteString(host)                                  //
	_ = binary.Write(&hs, binary.BigEndian, uint16(port)) //nolint:gosec // a port
	hs.Write(varint(1))                                   // next state: status
	var out bytes.Buffer
	out.Write(varint(int32(hs.Len()))) //nolint:gosec // small
	out.Write(hs.Bytes())
	out.Write([]byte{0x01, 0x00}) // status request
	if _, err := c.Write(out.Bytes()); err != nil {
		return Players{}, err
	}

	r := bufio.NewReader(io.LimitReader(c, 1<<20))
	if _, err := readVarint(r); err != nil { // packet length
		return Players{}, err
	}
	if id, err := readVarint(r); err != nil || id != 0 {
		return Players{}, errors.Join(errors.New("not a status response"), err)
	}
	n, err := readVarint(r)
	if err != nil || n < 0 || n > 1<<20 {
		return Players{}, errors.Join(errors.New("bad status length"), err)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Players{}, err
	}
	var st struct {
		Players struct {
			Online int `json:"online"`
			Max    int `json:"max"`
		} `json:"players"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return Players{}, err
	}
	return Players{Online: st.Players.Online, Max: st.Players.Max}, nil
}

func varint(v int32) []byte {
	u := uint32(v) //nolint:gosec // two's complement, as the protocol wants
	var b []byte
	for {
		if u&^0x7f == 0 {
			return append(b, byte(u))
		}
		b = append(b, byte(u&0x7f|0x80)) //nolint:gosec // masked to 7 bits
		u >>= 7
	}
}

func readVarint(r io.ByteReader) (int32, error) {
	var v uint32
	for i := range 5 {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		v |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return int32(v), nil //nolint:gosec // two's complement
		}
	}
	return 0, errors.New("varint too long")
}

// a2sInfo is the A2S_INFO request.
var a2sInfo = append([]byte{0xff, 0xff, 0xff, 0xff, 'T'}, "Source Engine Query\x00"...)

// querySource uses Valve's A2S_INFO over UDP, answering a challenge if the
// server asks for one.
func querySource(ctx context.Context, addr string) (Players, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return Players{}, err
	}
	defer func() { _ = c.Close() }()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	req := a2sInfo
	buf := make([]byte, 1400)
	for range 2 {
		if _, err := c.Write(req); err != nil {
			return Players{}, err
		}
		n, err := c.Read(buf)
		if err != nil {
			return Players{}, err
		}
		resp := buf[:n]
		if len(resp) < 5 || !bytes.Equal(resp[:4], []byte{0xff, 0xff, 0xff, 0xff}) {
			return Players{}, errors.New("not an A2S reply")
		}
		switch resp[4] {
		case 'A': // challenge: ask again with it
			if len(resp) < 9 {
				return Players{}, errors.New("short challenge")
			}
			req = append(append([]byte{}, a2sInfo...), resp[5:9]...)
			continue
		case 'I':
			return parseA2SInfo(resp[5:])
		}
		return Players{}, fmt.Errorf("unexpected A2S reply %q", resp[4])
	}
	return Players{}, errors.New("no A2S_INFO reply after the challenge")
}

func parseA2SInfo(b []byte) (Players, error) {
	r := bytes.NewReader(b)
	if _, err := r.ReadByte(); err != nil { // protocol
		return Players{}, err
	}
	for range 4 { // name, map, folder, game
		if _, err := readCString(r); err != nil {
			return Players{}, err
		}
	}
	var hdr struct {
		ID      uint16
		Players uint8
		Max     uint8
	}
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		return Players{}, err
	}
	return Players{Online: int(hdr.Players), Max: int(hdr.Max)}, nil
}

func readCString(r *bytes.Reader) (string, error) {
	var s []byte
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if c == 0 {
			return string(s), nil
		}
		s = append(s, c)
	}
}
