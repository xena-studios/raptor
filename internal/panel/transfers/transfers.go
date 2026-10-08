// Package transfers carries the web file manager's uploads and downloads
// between browsers and nodes (docs/ARCHITECTURE.md#files-and-sftp): the
// browser starts one with a command (files.upload, files.download), then
// its bytes go through here and over a transfer connection the node opens
// for it, never the node's main connection.
package transfers

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"path"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/nodes"
)

// Paths, under the API's prefix.
const (
	UploadPath   = "/files/upload"   // PUT ?node=&server=&upload=&offset=, the chunk as the body
	DownloadPath = "/files/download" // GET ?node=&server=&path=
)

// MaxChunk is the largest chunk a browser may send, under Cloudflare's
// 100 MB request limit (Wings accepts up to 64 MiB).
const MaxChunk = 64 << 20

// Opener opens transfer connections wherever the node is (*nodes.Router).
type Opener interface {
	OpenTransfer(ctx context.Context, nodeID, transferID string) (*nodes.Transfer, error)
}

// Handler serves uploads and downloads.
type Handler struct {
	Auth     *auth.Service
	Commands *commands.Service
	Opener   Opener

	mu    sync.Mutex
	open  map[string]*held // transfer connections kept between chunks
	start sync.Once
}

// held is a transfer connection kept for the next chunk, closed when it's
// been idle for idleClose.
type held struct {
	t    *nodes.Transfer
	used time.Time
	busy bool
}

const idleClose = time.Minute

// Register adds the routes to the API's mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("PUT "+UploadPath, h.upload)
	mux.HandleFunc("GET "+DownloadPath, h.download)
}

// run sends a command as the signed-in user (their access is checked, and
// the node checks the grant) and decodes its result.
func (h *Handler) run(ctx context.Context, node, server, action string, params, out any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	res, err := h.Commands.Execute(ctx, &panelv1.ExecuteRequest{NodeId: node, ServerId: server, Action: action, ParamsJson: string(p)})
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(res.GetResultJson()), out)
}

// transfer returns a connection for the transfer, reusing one kept from an
// earlier chunk. Call done when finished with it.
func (h *Handler) transfer(ctx context.Context, node, id string) (t *nodes.Transfer, done func(failed bool), err error) {
	h.start.Do(func() {
		h.open = map[string]*held{}
		go h.sweep()
	})
	key := node + "/" + id
	h.mu.Lock()
	if c := h.open[key]; c != nil && !c.busy {
		c.busy = true
		h.mu.Unlock()
		return c.t, h.release(key, c), nil
	}
	h.mu.Unlock()
	t, err = h.Opener.OpenTransfer(ctx, node, id)
	if err != nil {
		return nil, nil, err
	}
	c := &held{t: t, busy: true}
	h.mu.Lock()
	if old := h.open[key]; old != nil && !old.busy {
		_ = old.t.Close()
	}
	h.open[key] = c
	h.mu.Unlock()
	return t, h.release(key, c), nil
}

func (h *Handler) release(key string, c *held) func(bool) {
	return func(failed bool) {
		h.mu.Lock()
		defer h.mu.Unlock()
		c.busy, c.used = false, time.Now()
		if failed {
			_ = c.t.Close()
			if h.open[key] == c {
				delete(h.open, key)
			}
		}
	}
}

func (h *Handler) sweep() {
	for range time.Tick(idleClose / 2) {
		h.mu.Lock()
		for k, c := range h.open {
			if !c.busy && time.Since(c.used) > idleClose {
				_ = c.t.Close()
				delete(h.open, k)
			}
		}
		h.mu.Unlock()
	}
}

// fail answers an error as JSON, with a status for its Connect code.
func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	msg := err.Error()
	var ce *connect.Error
	if errors.As(err, &ce) {
		msg = ce.Message()
		switch ce.Code() {
		case connect.CodeUnauthenticated:
			status = http.StatusUnauthorized
		case connect.CodeNotFound:
			status = http.StatusNotFound
		case connect.CodePermissionDenied:
			status = http.StatusForbidden
		case connect.CodeInvalidArgument, connect.CodeFailedPrecondition, connect.CodeUnknown:
			// Unknown: the node ran a read-only command and it failed
			// (no such upload, no such file); its words say why.
			status = http.StatusBadRequest
		case connect.CodeResourceExhausted:
			status = http.StatusTooManyRequests
		case connect.CodeUnavailable:
			status = http.StatusServiceUnavailable
		}
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// upload takes one chunk of an upload the browser started with
// files.upload, at the offset the node has received up to. A chunk that
// doesn't start there answers 409 with the upload's state, to resume from.
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	ctx := auth.WithRequest(r.Context(), w, r)
	q := r.URL.Query()
	node, server, id := q.Get("node"), q.Get("server"), q.Get("upload")
	offset, err := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err != nil || offset < 0 || id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which upload, from which offset?"})
		return
	}
	// The user may write this server's files, and the upload is this
	// server's (Wings answers "not found" for another's).
	var st nodes.UploadState
	if err := h.run(ctx, node, server, "files.upload.status", map[string]string{"upload_id": id}, &st); err != nil {
		fail(w, err)
		return
	}
	if st.Done || offset != st.Received {
		writeJSON(w, http.StatusConflict, st)
		return
	}
	t, done, err := h.transfer(ctx, node, id)
	if err != nil {
		fail(w, connect.NewError(connect.CodeUnavailable, err))
		return
	}
	st, err = t.Upload(ctx, offset, http.MaxBytesReader(w, r.Body, MaxChunk))
	done(err != nil)
	if err != nil {
		if errors.Is(err, nodes.ErrTransfer) {
			writeJSON(w, http.StatusConflict, st)
			return
		}
		fail(w, connect.NewError(connect.CodeUnavailable, err))
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// downloadInfo is files.download's result (Wings' files.Download).
type downloadInfo struct {
	ID       string `json:"download_id"`
	Size     int64  `json:"size"`
	MaxChunk int64  `json:"max_chunk"`
}

// download sends a whole file as one response, chunk by chunk from the
// node; the browser saves it (Content-Disposition: attachment).
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	ctx := auth.WithRequest(r.Context(), w, r)
	q := r.URL.Query()
	node, server, name := q.Get("node"), q.Get("server"), q.Get("path")
	var d downloadInfo
	if err := h.run(ctx, node, server, "files.download", map[string]string{"path": name}, &d); err != nil {
		fail(w, err)
		return
	}
	t, done, err := h.transfer(ctx, node, d.ID)
	if err != nil {
		fail(w, connect.NewError(connect.CodeUnavailable, err))
		return
	}
	failed := true
	defer func() { done(failed) }()
	chunk := min(d.MaxChunk, MaxChunk)
	if chunk <= 0 {
		chunk = 16 << 20
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "application/octet-stream")
	hdr.Set("Content-Length", strconv.FormatInt(d.Size, 10))
	hdr.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	for offset := int64(0); offset < d.Size; {
		n, err := t.Download(ctx, offset, chunk, d.Size, w)
		offset += n
		if err != nil {
			// The status is sent; cutting the response short makes the
			// browser report the download as failed.
			panic(http.ErrAbortHandler)
		}
	}
	failed = false
}
