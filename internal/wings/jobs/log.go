package jobs

import (
	"os"
	"sync"
	"time"
)

// flushEvery bounds how much output a Wings crash can lose.
const flushEvery = 5 * time.Second

// jobLog keeps the last LogLimit bytes of a job's output in memory and
// writes them to disk every few seconds and when the job ends. It's safe for
// concurrent use.
type jobLog struct {
	path string

	mu        sync.Mutex
	buf       []byte
	dirty     bool
	truncated bool
	stop      chan struct{}
	stopped   chan struct{}
}

func newJobLog(path string) (*jobLog, error) {
	l := &jobLog{path: path, stop: make(chan struct{}), stopped: make(chan struct{})}
	go l.flusher()
	return l, nil
}

func (l *jobLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > 2*LogLimit {
		l.buf = append([]byte(nil), l.buf[len(l.buf)-LogLimit:]...)
		l.truncated = true
	}
	l.dirty = true
	return len(p), nil
}

// bytes returns the kept output.
func (l *jobLog) bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buf
	if len(b) > LogLimit {
		b = b[len(b)-LogLimit:]
	}
	out := make([]byte, 0, len(b)+64)
	if l.truncated || len(l.buf) > LogLimit {
		out = append(out, "[raptor] earlier output was dropped (log limit is 10 MB)\n"...)
	}
	return append(out, b...)
}

func (l *jobLog) flusher() {
	defer close(l.stopped)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			l.flush()
		case <-l.stop:
			l.flush()
			return
		}
	}
}

func (l *jobLog) flush() {
	l.mu.Lock()
	if !l.dirty {
		l.mu.Unlock()
		return
	}
	l.dirty = false
	l.mu.Unlock()
	b := l.bytes()
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		_ = os.Rename(tmp, l.path)
	}
}

// close writes the final log and stops the flusher.
func (l *jobLog) close() {
	close(l.stop)
	<-l.stopped
}
