package transfers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
)

// QueryPath runs a database browser's SQL and answers its results as they
// come (docs/DATABASES.md#the-browser): POST {"node", "server", "action",
// "params"} with action db.query or db.query.read. The command starts the
// query on the node (the user's access is checked, and the node checks
// the grant); its results then come over a transfer connection, like a
// download's bytes, as lines of JSON: first {"query_id": …} (for
// db.query.cancel), then the node's (databases.StreamQuery). Closing the
// response stops the query on the engine.
const QueryPath = "/databases/query"

const (
	maxQueryBody = 2 << 20  // the SQL is at most 1 MB
	maxQueryLine = 16 << 20 // one line from the node: a batch of rows
)

type queryRequest struct {
	Node   string         `json:"node"`
	Server string         `json:"server"`
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
}

func (h *Handler) query(w http.ResponseWriter, r *http.Request) {
	ctx := auth.WithRequest(r.Context(), w, r)
	var req queryRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQueryBody)).Decode(&req); err != nil || req.Params == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which query, on which database?"})
		return
	}
	if req.Action != "db.query" && req.Action != "db.query.read" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only db.query and db.query.read stream"})
		return
	}
	req.Params["stream"] = true
	params, err := json.Marshal(req.Params)
	if err != nil {
		fail(w, err)
		return
	}
	res, record, err := h.Commands.ExecuteThenRecord(ctx, &panelv1.ExecuteRequest{
		NodeId: req.Node, ServerId: req.Server, Action: req.Action, ParamsJson: string(params),
	})
	if err != nil {
		fail(w, err)
		return
	}
	if record == nil {
		record = func(map[string]any) {}
	}
	var started struct {
		QueryID string `json:"query_id"`
	}
	_ = json.Unmarshal([]byte(res.GetResultJson()), &started)
	hdr := w.Header()
	hdr.Set("Content-Type", "application/x-ndjson")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)

	if started.QueryID == "" {
		// A Wings from before streaming ran it whole and answered its
		// result: send that as lines too.
		w.WriteHeader(http.StatusOK)
		record(asLines(w, []byte(res.GetResultJson())))
		return
	}
	t, err := h.Opener.OpenTransfer(ctx, req.Node, started.QueryID)
	if err != nil {
		record(map[string]any{"error": err.Error()})
		fail(w, connect.NewError(connect.CodeUnavailable, err))
		return
	}
	defer func() { _ = t.Close() }()
	body, err := t.Query(ctx)
	if err != nil {
		record(map[string]any{"error": err.Error()})
		fail(w, connect.NewError(connect.CodeUnavailable, err))
		return
	}
	defer func() { _ = body.Close() }()
	w.WriteHeader(http.StatusOK)
	first, _ := json.Marshal(started)
	_, _ = w.Write(append(first, '\n'))
	_ = rc.Flush()

	outcome := map[string]any{}
	in := bufio.NewReaderSize(body, 64<<10)
	for {
		line, err := readLine(in)
		if len(line) > 0 {
			noteOutcome(line, outcome)
			if _, werr := w.Write(line); werr != nil {
				outcome["error"] = "the browser closed the stream"
				break
			}
			_ = rc.Flush()
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				msg := "the node's stream broke: " + err.Error()
				if ctx.Err() != nil {
					msg = "the browser closed the stream"
				}
				b, _ := json.Marshal(map[string]string{"error": msg})
				_, _ = w.Write(append(b, '\n'))
				outcome["error"] = msg
			}
			break
		}
	}
	record(outcome)
}

// readLine reads one line, with its newline, of at most maxQueryLine.
func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		part, err := r.ReadSlice('\n')
		out = append(out, part...)
		if len(out) > maxQueryLine {
			return nil, errors.New("a line was too long")
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return out, err
		}
	}
}

// noteOutcome keeps what the activity records of a query from its lines:
// how many rows it changed, or why it failed. Never its rows.
func noteOutcome(line []byte, out map[string]any) {
	switch {
	case bytes.HasPrefix(line, []byte(`{"done":`)):
		var d struct {
			Done struct {
				Affected *int64 `json:"affected"`
			} `json:"done"`
		}
		if json.Unmarshal(line, &d) == nil && d.Done.Affected != nil && *d.Done.Affected >= 0 {
			out["rows_changed"] = *d.Done.Affected
		}
	case bytes.HasPrefix(line, []byte(`{"error":`)):
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(line, &e) == nil {
			out["error"] = e.Error
		}
	}
}

// asLines writes a whole query result (databases.Result) as a stream's
// lines, and returns what the activity records of it.
func asLines(w io.Writer, result []byte) map[string]any {
	var res struct {
		Columns    json.RawMessage   `json:"columns"`
		Rows       []json.RawMessage `json:"rows"`
		More       bool              `json:"more"`
		Cut        bool              `json:"cut"`
		Affected   int64             `json:"affected"`
		Statements int               `json:"statements"`
		Millis     int64             `json:"millis"`
	}
	if err := json.Unmarshal(result, &res); err != nil {
		b, _ := json.Marshal(map[string]string{"error": "the node's answer wasn't a result"})
		_, _ = w.Write(append(b, '\n'))
		return map[string]any{"error": "the node's answer wasn't a result"}
	}
	send := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = w.Write(append(b, '\n'))
	}
	if len(res.Columns) > 0 && string(res.Columns) != "null" {
		send(map[string]any{"columns": res.Columns})
		if len(res.Rows) > 0 {
			send(map[string]any{"rows": res.Rows})
		}
	}
	send(map[string]any{"done": map[string]any{
		"statements": res.Statements, "affected": res.Affected, "rows": len(res.Rows), "shown": len(res.Rows),
		"more": res.More, "cut": res.Cut, "millis": res.Millis,
	}})
	out := map[string]any{}
	if res.Affected >= 0 {
		out["rows_changed"] = res.Affected
	}
	return out
}
