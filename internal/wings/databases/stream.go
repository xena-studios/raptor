package databases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Streamed queries (docs/DATABASES.md#the-browser): the SQL editor's
// results come over a transfer connection, like the file manager's
// downloads, not in a command's answer. db.query (or db.query.read) with
// stream starts one: it checks the SQL, opens the session, and answers its
// ID; the Panel then opens a transfer connection for that ID and reads the
// results as they come, as lines of JSON (StreamQuery). db.query.cancel
// stops it on the engine.
const (
	MaxStreamRows    = 10000    // rows of one result shown; the rest are counted
	maxStreamBytes   = 32 << 20 // bytes of values shown, then the rest are counted
	MaxStreamTimeout = 600      // seconds a streamed query may run
	streamConnect    = 30 * time.Second
	maxRuns          = 16 // queries started or running on the node at once
	maxUserRuns      = 4  // and per user
	batchRows        = 500
	batchBytes       = 256 << 10
	streamTick       = 200 * time.Millisecond
	streamWait       = 10 * time.Second // a line at least this often, so proxies keep the response open
)

// queryRun is a query started and waiting for its stream, or running.
type queryRun struct {
	id, user, serverID string
	se                 *session
	sql                string
	timeout            int
	readOnly           bool
	started            bool
	cancel             context.CancelCauseFunc
}

type queryRuns struct {
	mu sync.Mutex
	m  map[string]*queryRun
}

// StartQuery checks SQL to run on one of a server's databases and opens
// its session (as the database's reader when readOnly, otherwise as its
// own user), then waits up to streamConnect for StreamQuery to run it.
func (s *Service) StartQuery(ctx context.Context, serverID, id, user string, in QueryInput, readOnly bool) (string, error) {
	text, timeout, err := in.check(MaxStreamTimeout)
	if err != nil {
		return "", err
	}
	if err := s.runs.room(user); err != nil {
		return "", err
	}
	var se *session
	if readOnly {
		se, err = s.openViewer(ctx, serverID, id)
	} else {
		se, err = s.open(ctx, serverID, id, true)
	}
	if err != nil {
		return "", err
	}
	q := &queryRun{id: uuid.NewString(), user: user, serverID: serverID, se: se, sql: text, timeout: timeout, readOnly: readOnly}
	s.runs.mu.Lock()
	if s.runs.m == nil {
		s.runs.m = map[string]*queryRun{}
	}
	s.runs.m[q.id] = q
	s.runs.mu.Unlock()
	time.AfterFunc(streamConnect, func() {
		s.runs.mu.Lock()
		defer s.runs.mu.Unlock()
		if cur := s.runs.m[q.id]; cur == q && !q.started {
			delete(s.runs.m, q.id)
			q.se.Close()
		}
	})
	return q.id, nil
}

// room checks another query may start.
func (r *queryRuns) room(user string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	mine := 0
	for _, q := range r.m {
		if q.user == user {
			mine++
		}
	}
	if len(r.m) >= maxRuns || mine >= maxUserRuns {
		return invalid{"too many queries are running; wait for one to finish, or cancel it"}
	}
	return nil
}

// HasQuery reports whether id is a query waiting for its stream.
func (s *Service) HasQuery(id string) bool {
	s.runs.mu.Lock()
	defer s.runs.mu.Unlock()
	q, ok := s.runs.m[id]
	return ok && !q.started
}

// ErrNoQuery is a stream for a query that isn't waiting (it ran, expired,
// or was cancelled).
var ErrNoQuery = errors.New("the query isn't waiting to run: it ran, was cancelled, or wasn't asked for in time")

// StreamQuery runs a started query, writing its results to w as lines of
// JSON, and calling flush after each:
//
//	{"columns": [{"name": …, "type": …}]}   a result with rows begins
//	{"rows": [[…], …]}                      some of its rows (null is NULL)
//	{"wait": true}                          still running
//	{"done": {"statements", "affected", "rows", "shown", "more", "cut", "millis"}}
//	{"error": "…"}                          it failed, or was stopped
//
// A result shows its first MaxStreamRows rows; the rest are read and
// counted (rows), so later statements still run. ctx ending (the Panel
// closed the stream) stops the query on the engine, like
// db.query.cancel.
func (s *Service) StreamQuery(ctx context.Context, id string, w io.Writer, flush func()) error {
	s.runs.mu.Lock()
	q, ok := s.runs.m[id]
	if !ok || q.started {
		s.runs.mu.Unlock()
		return ErrNoQuery
	}
	q.started = true
	qctx, cancel := context.WithCancelCause(ctx)
	q.cancel = cancel
	s.runs.mu.Unlock()
	defer func() {
		cancel(nil)
		q.se.Close()
		s.runs.mu.Lock()
		delete(s.runs.m, id)
		s.runs.mu.Unlock()
	}()

	tctx, stop := context.WithTimeout(qctx, time.Duration(q.timeout)*time.Second)
	defer stop()
	out := &lines{w: w, flush: flush, last: time.Now()}
	tick := time.NewTicker(streamTick)
	ticking := make(chan struct{})
	go func() {
		defer close(ticking)
		for {
			select {
			case <-tick.C:
				out.tick()
			case <-tctx.Done():
				return
			}
		}
	}()
	start := time.Now()
	done, err := q.se.run(tctx, q.sql, q.readOnly, q.timeout, out)
	if err == nil && tctx.Err() != nil {
		// Stopped, though the engine said nothing (MariaDB and MySQL's
		// SLEEP just returns early when killed).
		err = context.Cause(tctx)
	}
	tick.Stop()
	stop()
	<-ticking
	if err != nil {
		err = stopped(tctx, err, q.timeout)
		out.send(map[string]string{"error": err.Error()})
		return out.err
	}
	out.finish(done, time.Since(start).Milliseconds())
	return out.err
}

// CancelQuery stops a query its user started on one of a server's
// databases, on the engine.
func (s *Service) CancelQuery(serverID, user, id string) error {
	s.runs.mu.Lock()
	q, ok := s.runs.m[id]
	if !ok || q.serverID != serverID || q.user != user {
		s.runs.mu.Unlock()
		return fmt.Errorf("%w: query %s", ErrNotFound, id)
	}
	if !q.started {
		delete(s.runs.m, id)
		s.runs.mu.Unlock()
		q.se.Close()
		return nil
	}
	cancel := q.cancel
	s.runs.mu.Unlock()
	cancel(errCancelled)
	return nil
}

// lines is a sink writing a query's results as lines of JSON, rows in
// batches.
type lines struct {
	mu    sync.Mutex
	w     io.Writer
	flush func()
	last  time.Time
	err   error

	batch [][]*string
	size  int // bytes in batch
	shown int
	total int64
	bytes int
	more  bool
	cut   bool
}

// send writes one line. Call with mu held, or when nothing else writes.
func (l *lines) send(v any) {
	if l.err != nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		l.err = err
		return
	}
	if _, err := l.w.Write(append(b, '\n')); err != nil {
		l.err = err
		return
	}
	if l.flush != nil {
		l.flush()
	}
	l.last = time.Now()
}

func (l *lines) sendBatch() {
	if len(l.batch) == 0 {
		return
	}
	l.send(map[string]any{"rows": l.batch})
	l.batch, l.size = nil, 0
}

func (l *lines) columns(cols []ResultColumn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sendBatch()
	l.shown, l.total, l.bytes, l.more, l.cut = 0, 0, 0, false, false
	l.send(map[string]any{"columns": cols})
}

func (l *lines) row(row []*string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	l.total++
	if l.shown >= MaxStreamRows || l.bytes > maxStreamBytes {
		l.more = true
		return nil
	}
	n, cut := clean(row)
	l.cut = l.cut || cut
	l.shown++
	l.bytes += n
	l.size += n
	l.batch = append(l.batch, row)
	if len(l.batch) >= batchRows || l.size >= batchBytes {
		l.sendBatch()
	}
	return l.err
}

// tick sends the rows waiting, or a line saying it's still running.
func (l *lines) tick() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.batch) > 0 {
		l.sendBatch()
	} else if time.Since(l.last) >= streamWait {
		l.send(map[string]bool{"wait": true})
	}
}

func (l *lines) finish(r ran, millis int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sendBatch()
	l.send(map[string]any{"done": map[string]any{
		"statements": r.Statements, "affected": r.Affected, "rows": l.total, "shown": l.shown,
		"more": l.more, "cut": l.cut, "millis": millis,
	}})
}
