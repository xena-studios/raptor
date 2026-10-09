package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// maxLine is the longest line Logs returns; the rest is cut off.
const maxLine = 8 << 10

type input struct{ conn io.WriteCloser }

func (i *input) Send(cmd string) error {
	_, err := io.WriteString(i.conn, cmd+"\n")
	return err
}

func (i *input) Close() error { return i.conn.Close() }

// Input attaches to the container's stdin only. Closing it doesn't close the
// container's stdin (StdinOnce is off), so the game never sees EOF.
func (c *Client) Input(ctx context.Context, id string) (containers.Input, error) {
	res, err := c.api.ContainerAttach(ctx, id, client.ContainerAttachOptions{Stream: true, Stdin: true})
	if err != nil {
		return nil, err
	}
	return &input{conn: res.Conn}, nil
}

// Logs streams output with Docker's timestamps, so a caller can resume from
// the last line it saw. Server containers use a TTY, so the stream isn't
// multiplexed.
func (c *Client) Logs(ctx context.Context, id string, o containers.LogOptions) (<-chan containers.Line, <-chan error) {
	lines := make(chan containers.Line, 256)
	errc := make(chan error, 1)
	opts := client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: o.Follow, Tail: "all"}
	if o.Tail >= 0 {
		opts.Tail = strconv.Itoa(o.Tail)
	}
	if !o.Since.IsZero() {
		opts.Since = strconv.FormatInt(o.Since.Unix(), 10) + "." + fmt.Sprintf("%09d", o.Since.Nanosecond())
	}
	go func() {
		defer close(lines)
		rc, err := c.api.ContainerLogs(ctx, id, opts)
		if err != nil {
			errc <- err
			return
		}
		defer func() { _ = rc.Close() }()
		errc <- readLines(ctx, rc, lines)
	}()
	return lines, errc
}

// readLines splits "<RFC3339Nano timestamp> <text>" lines. Over-long lines
// are truncated rather than buffered without bound.
func readLines(ctx context.Context, r io.Reader, out chan<- containers.Line) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		raw, err := readLine(br)
		if len(raw) > 0 || err == nil {
			ts, text, _ := strings.Cut(raw, " ")
			t, perr := time.Parse(time.RFC3339Nano, ts)
			if perr != nil {
				text = raw // not timestamped; keep the whole line
			}
			text = strings.TrimRight(text, "\r")
			if len(text) > maxLine {
				text = text[:maxLine]
			}
			select {
			case out <- containers.Line{Time: t, Text: text}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readLine reads one line, keeping at most maxLine+64 bytes of it (enough
// for the timestamp) and discarding the rest.
func readLine(br *bufio.Reader) (string, error) {
	var b []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(b) < maxLine+64 {
			b = append(b, chunk[:min(len(chunk), maxLine+64-len(b))]...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return strings.TrimSuffix(string(b), "\n"), err
	}
}

// Inspect reports the container's state.
func (c *Client) Inspect(ctx context.Context, id string) (containers.State, error) {
	res, err := c.api.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return containers.State{}, err
	}
	st := res.Container.State
	if st == nil {
		return containers.State{}, errors.New("container has no state")
	}
	started, _ := time.Parse(time.RFC3339Nano, st.StartedAt)
	return containers.State{Running: st.Running, ExitCode: int64(st.ExitCode), OOMKilled: st.OOMKilled, StartedAt: started, Pid: st.Pid}, nil
}

// Stats samples a container's usage. One-shot: Docker returns immediately
// instead of waiting a second for a second sample.
func (c *Client) Stats(ctx context.Context, id string) (containers.Stats, error) {
	res, err := c.api.ContainerStats(ctx, id, client.ContainerStatsOptions{})
	if err != nil {
		return containers.Stats{}, err
	}
	defer func() { _ = res.Body.Close() }()
	var s container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		return containers.Stats{}, err
	}
	// Like `docker stats`: page cache the kernel can reclaim isn't counted.
	mem := s.MemoryStats.Usage
	if cache := s.MemoryStats.Stats["inactive_file"]; cache < mem {
		mem -= cache
	}
	out := containers.Stats{
		Time:        s.Read,
		CPUNanos:    s.CPUStats.CPUUsage.TotalUsage,
		MemoryBytes: int64(min(mem, math.MaxInt64)), //nolint:gosec // bounded above
	}
	for _, n := range s.Networks {
		out.RxBytes += n.RxBytes
		out.TxBytes += n.TxBytes
	}
	return out, nil
}
