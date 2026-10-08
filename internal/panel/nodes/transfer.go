package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Transfer is a connection Wings opened for one upload or download
// (docs/ARCHITECTURE.md#files-and-sftp): its chunks travel here, never on the
// node's main connection, so a large file doesn't slow the console.
type Transfer struct {
	ID      string
	Session *nodelink.Session
}

// UploadState is where an upload is, as Wings reports it.
type UploadState struct {
	Received int64  `json:"received"`
	Size     int64  `json:"size"`
	Done     bool   `json:"done"`
	MaxChunk int64  `json:"max_chunk"`
	Error    string `json:"error"`
}

// The transfer connection's routes (internal/wings/link).
const (
	uploadPath   = "/upload"
	downloadPath = "/download"
)

// ErrTransfer is a chunk Wings refused or couldn't complete.
var ErrTransfer = errors.New("transfer failed")

// OpenTransfer asks the node to open a connection for a transfer that a
// files.upload or files.download command started, and returns it once it's
// up. The node must be connected to this instance (Router.OpenTransfer
// asks through the instance holding it).
func (h *Hub) OpenTransfer(ctx context.Context, nodeID, transferID string) (*Transfer, error) {
	return h.openTransfer(ctx, nodeID, transferID, func(ctx context.Context) error {
		c, ok := h.Conn(nodeID)
		if !ok {
			return errors.New("node is offline")
		}
		_, err := c.Node.OpenTransfer(ctx, &nodev1.OpenTransferRequest{TransferId: transferID, Route: h.Route})
		return err
	})
}

// openTransfer waits for the transfer connection that ask gets the node to
// open, to this instance.
func (h *Hub) openTransfer(ctx context.Context, nodeID, transferID string, ask func(context.Context) error) (*Transfer, error) {
	h.init()
	key := nodeID + "/" + transferID
	ch := make(chan *nodelink.Session, 1)
	h.mu.Lock()
	if h.pending == nil {
		h.pending = map[string]chan *nodelink.Session{}
	}
	if _, busy := h.pending[key]; busy {
		h.mu.Unlock()
		return nil, fmt.Errorf("transfer %s is already being opened", transferID)
	}
	h.pending[key] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.pending, key)
		h.mu.Unlock()
	}()

	if err := ask(ctx); err != nil {
		return nil, err
	}
	// Wings answers once its connection is up, so it's here or about to be.
	select {
	case s := <-ch:
		return &Transfer{ID: transferID, Session: s}, nil
	case <-time.After(nodelink.HandshakeTimeout):
		return nil, errors.New("the transfer connection didn't arrive")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// acceptTransfer hands a transfer connection to the OpenTransfer waiting for
// it. One nobody asked for is closed.
func (h *Hub) acceptTransfer(s *nodelink.Session, id string) bool {
	h.mu.Lock()
	ch, ok := h.pending[s.Hello.NodeID+"/"+id]
	h.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- s:
		return true
	default:
		return false
	}
}

// Upload sends a chunk starting at offset, which must be what Wings has
// received so far (a mismatch is an error with the state to resume from).
func (t *Transfer) Upload(ctx context.Context, offset int64, chunk io.Reader) (UploadState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, nodelink.BaseURL+uploadPath+"?offset="+strconv.FormatInt(offset, 10), chunk)
	if err != nil {
		return UploadState{}, err
	}
	resp, err := t.Session.Client().Do(req)
	if err != nil {
		return UploadState{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var st UploadState
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&st); err != nil {
		return UploadState{}, fmt.Errorf("upload answer (%s): %w", resp.Status, err)
	}
	if resp.StatusCode != http.StatusOK {
		return st, fmt.Errorf("%w: %s", ErrTransfer, st.Error)
	}
	return st, nil
}

// Download copies up to n bytes from offset to w. A short read (the
// connection dropped, or the file changed partway) is an error; the caller
// resumes from offset plus what was written.
func (t *Transfer) Download(ctx context.Context, offset, n int64, size int64, w io.Writer) (int64, error) {
	q := url.Values{"offset": {strconv.FormatInt(offset, 10)}, "n": {strconv.FormatInt(n, 10)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nodelink.BaseURL+downloadPath+"?"+q.Encode(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := t.Session.Client().Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("%w: %s", ErrTransfer, strings.TrimSpace(string(b)))
	}
	got, err := io.Copy(w, resp.Body)
	if want := min(n, size-offset); err == nil && got != want {
		err = fmt.Errorf("%w: got %d of %d bytes", ErrTransfer, got, want)
	}
	return got, err
}

// Close ends the transfer connection.
func (t *Transfer) Close() error { return t.Session.Close() }

// OpenTransfer opens a transfer connection to this instance wherever the
// node is connected: if another instance holds it, that one asks the node
// (a forwarded request) to open the connection to this instance's Route,
// so the transfer's bytes never pass between instances.
func (r *Router) OpenTransfer(ctx context.Context, nodeID, transferID string) (*Transfer, error) {
	if _, ok := r.Hub.Conn(nodeID); ok {
		return r.Hub.OpenTransfer(ctx, nodeID, transferID)
	}
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad node ID"))
	}
	holder, err := r.q().NodeHolder(ctx, store.NodeHolderParams{NodeID: pgUUID(id), StaleSecs: staleAfter.Seconds()})
	if err != nil || holder == r.ID {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the node is offline"))
	}
	req, err := proto.Marshal(&nodev1.OpenTransferRequest{TransferId: transferID, Route: r.Hub.Route})
	if err != nil {
		return nil, err
	}
	return r.Hub.openTransfer(ctx, nodeID, transferID, func(ctx context.Context) error {
		_, err := r.forwardRaw(ctx, holder, id, methodOpenTransfer, req)
		return err
	})
}
