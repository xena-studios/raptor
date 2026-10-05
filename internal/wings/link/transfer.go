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

// transferIdle closes a transfer connection the Panel stopped using.
const transferIdle = 10 * time.Minute

// The transfer connection's routes. It serves its own transfer only.
const (
	UploadPath   = "/upload"   // PUT ?offset=N, the chunk as the body; answers the upload's state (JSON)
	DownloadPath = "/download" // GET ?offset=N&n=M, answers the bytes
)

func (s *service) OpenTransfer(ctx context.Context, req *nodev1.OpenTransferRequest) (*nodev1.OpenTransferResponse, error) {
	var t Transfers
	if s.l.cfg.Transfers != nil {
		t = s.l.cfg.Transfers()
	}
	if t == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the node is starting up (its container runtime isn't ready)"))
	}
	id := req.GetTransferId()
	if id == "" || len(id) > 64 || !t.HasTransfer(ctx, id) {
		return nil, connect.NewError(connect.CodeNotFound, files.ErrTransferNotFound)
	}
	dctx, cancel := context.WithTimeout(ctx, nodelink.HandshakeTimeout)
	defer cancel()
	sess, err := nodelink.Dial(context.WithoutCancel(dctx), nodelink.DialConfig{
		URL: s.l.cfg.PanelURL, NodeID: s.l.cfg.NodeID, NodeKey: s.l.cfg.NodeKey, PanelKey: s.l.cfg.PanelKey,
		Purpose: nodelink.TransferPurpose(id), Software: s.l.cfg.Software, HTTPClient: s.l.cfg.HTTPClient, Keepalive: s.l.cfg.Keepalive,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("opening the transfer connection: %w", err))
	}
	go s.l.serveTransfer(sess, t, id)
	return &nodev1.OpenTransferResponse{}, nil
}

// serveTransfer answers one transfer's chunks until the Panel closes the
// connection or stops using it.
func (l *Link) serveTransfer(s *nodelink.Session, t Transfers, id string) {
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
