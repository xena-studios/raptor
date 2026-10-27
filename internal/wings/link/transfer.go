package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/files"
)

// Transfers is the part of the file service transfer connections use
// (*files.Service).
type Transfers interface {
	HasTransfer(ctx context.Context, id string) bool
	WriteChunk(ctx context.Context, uploadID string, offset int64, r io.Reader) (files.Upload, error)
	ReadChunk(ctx context.Context, downloadID string, offset, n int64, w io.Writer) (int64, error)
}

// Queries is the part of the database service transfer connections use
// for the database browser's streamed queries (*databases.Service).
type Queries interface {
	HasQuery(id string) bool
	StreamQuery(ctx context.Context, id string, w io.Writer, flush func()) error
}

// transferIdle closes a transfer connection the Panel stopped using.
const transferIdle = 10 * time.Minute

// The transfer connection's routes. It serves its own transfer only.
const (
	UploadPath   = "/upload"   // PUT ?offset=N, the chunk as the body; answers the upload's state (JSON)
	DownloadPath = "/download" // GET ?offset=N&n=M, answers the bytes
	QueryPath    = "/query"    // GET: runs the query, answering its results as lines of JSON
)

func (s *service) OpenTransfer(ctx context.Context, req *nodev1.OpenTransferRequest) (*nodev1.OpenTransferResponse, error) {
	var t Transfers
	if s.l.cfg.Transfers != nil {
		t = s.l.cfg.Transfers()
	}
	var q Queries
	if s.l.cfg.Queries != nil {
		q = s.l.cfg.Queries()
	}
	if t == nil && q == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the node is starting up (its container runtime isn't ready)"))
	}
	id := req.GetTransferId()
	if id == "" || len(id) > 64 {
		return nil, connect.NewError(connect.CodeNotFound, files.ErrTransferNotFound)
	}
	switch {
	case t != nil && t.HasTransfer(ctx, id):
		q = nil
	case q != nil && q.HasQuery(id):
		t = noTransfers{}
	default:
		return nil, connect.NewError(connect.CodeNotFound, files.ErrTransferNotFound)
	}
	route := req.GetRoute()
	if route != "" && !nodelink.ValidRoute(route) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad route"))
	}
	dctx, cancel := context.WithTimeout(ctx, nodelink.HandshakeTimeout)
	defer cancel()
	sess, err := nodelink.Dial(context.WithoutCancel(dctx), nodelink.DialConfig{
		URL: nodelink.WithRoute(s.l.cfg.PanelURL, route), NodeID: s.l.cfg.NodeID, NodeKey: s.l.cfg.NodeKey, PanelKey: s.l.cfg.PanelKey,
		Purpose: nodelink.TransferPurpose(id), Software: s.l.cfg.Software, HTTPClient: s.l.cfg.HTTPClient, Keepalive: s.l.cfg.Keepalive,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("opening the transfer connection: %w", err))
	}
	go s.l.serveTransfer(sess, t, q, id)
	return &nodev1.OpenTransferResponse{}, nil
}

// serveTransfer answers one transfer's chunks (or one query's results,
// with q) until the Panel closes the connection or stops using it.
func (l *Link) serveTransfer(s *nodelink.Session, t Transfers, q Queries, id string) {
	defer func() { _ = s.Close() }()
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	touch := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			last.Store(time.Now().UnixNano())
			defer last.Store(time.Now().UnixNano())
			h(w, r)
		}
	}
	mux := http.NewServeMux()
	if q != nil {
		mux.HandleFunc("GET "+QueryPath, touch(func(w http.ResponseWriter, r *http.Request) {
			rc := http.NewResponseController(w)
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.WriteHeader(http.StatusOK)
			// The answer starts now, not with the first line: a query can
			// take minutes to return its first row, and the Panel sends the
			// browser its first line (the query's ID) once this arrives.
			_ = rc.Flush()
			// Every line counts as use, so a long query isn't idle.
			flush := func() {
				last.Store(time.Now().UnixNano())
				_ = rc.Flush()
			}
			if err := q.StreamQuery(r.Context(), id, w, flush); err != nil {
				l.log.Info("a query's stream ended early", "query", id, "err", err)
			}
		}))
	}
	mux.HandleFunc("PUT "+UploadPath, touch(func(w http.ResponseWriter, r *http.Request) {
		offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if err != nil {
			http.Error(w, "bad offset", http.StatusBadRequest)
			return
		}
		up, err := t.WriteChunk(r.Context(), id, offset, r.Body)
		status := http.StatusOK
		if err != nil {
			status = transferStatus(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(struct {
			files.Upload
			Error string `json:"error,omitempty"`
		}{up, errString(err)})
	}))
	mux.HandleFunc("GET "+DownloadPath, touch(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		offset, err1 := strconv.ParseInt(q.Get("offset"), 10, 64)
		n, err2 := strconv.ParseInt(q.Get("n"), 10, 64)
		if err1 != nil || err2 != nil {
			http.Error(w, "bad offset or length", http.StatusBadRequest)
			return
		}
		// Errors found before the first byte get a status; one after it
		// cuts the response short, which the Panel sees as a short read.
		cw := &countingWriter{w: w}
		if _, err := t.ReadChunk(r.Context(), id, offset, n, cw); err != nil && cw.n == 0 {
			http.Error(w, err.Error(), transferStatus(err))
		}
	}))
	go func() { _ = s.Serve(mux) }()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-s.Done():
			return
		case <-tick.C:
			if time.Since(time.Unix(0, last.Load())) > transferIdle {
				l.log.Info("closing an idle transfer connection", "transfer", id)
				return
			}
		}
	}
}

// noTransfers answers a query's connection's file routes.
type noTransfers struct{}

func (noTransfers) HasTransfer(context.Context, string) bool { return false }

func (noTransfers) WriteChunk(context.Context, string, int64, io.Reader) (files.Upload, error) {
	return files.Upload{}, files.ErrTransferNotFound
}

func (noTransfers) ReadChunk(context.Context, string, int64, int64, io.Writer) (int64, error) {
	return 0, files.ErrTransferNotFound
}

func transferStatus(err error) int {
	switch {
	case errors.Is(err, files.ErrTransferNotFound):
		return http.StatusNotFound
	case errors.Is(err, files.ErrOffset), errors.Is(err, files.ErrChanged):
		return http.StatusConflict
	case errors.Is(err, files.ErrChunkTooLarge):
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusUnprocessableEntity
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	return n, err
}
