package rcon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

// webConn is Rust's WebRCON: a websocket at ws://host:port/<password>,
// carrying {"Identifier", "Message", "Name"} both ways. The game also
// sends its log and chat on it, with other identifiers.
type webConn struct {
	c  *websocket.Conn
	id int
}

type webMessage struct {
	Identifier int    `json:"Identifier"`
	Message    string `json:"Message"`
	Name       string `json:"Name,omitempty"`
	Type       string `json:"Type,omitempty"`
}

func dialWeb(ctx context.Context, addr, password string) (*webConn, error) {
	u := url.URL{Scheme: "ws", Host: addr, Path: "/" + password}
	c, res, err := websocket.Dial(ctx, u.String(), nil)
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		// Rust refuses a wrong password by closing the handshake.
		if res != nil && (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) {
			return nil, ErrAuth
		}
		// The password is in the URL; never repeat it.
		return nil, errors.New(hideSecret(err.Error(), password))
	}
	c.SetReadLimit(maxReply)
	return &webConn{c: c, id: 1000}, nil
}

func (w *webConn) Close() error { return w.c.Close(websocket.StatusNormalClosure, "") }

func (w *webConn) Exec(ctx context.Context, cmd string) (string, error) {
	if strings.ContainsAny(cmd, "\x00\n\r") {
		return "", errors.New("rcon: a command is one line")
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	w.id++
	b, err := json.Marshal(webMessage{Identifier: w.id, Message: cmd, Name: "Raptor"})
	if err != nil {
		return "", err
	}
	if err := w.c.Write(ctx, websocket.MessageText, b); err != nil {
		return "", fmt.Errorf("rcon: %w", err)
	}
	for {
		_, data, err := w.c.Read(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return "", errors.New("rcon: the game didn't answer in time")
			}
			return "", fmt.Errorf("rcon: %w", err)
		}
		var m webMessage
		if json.Unmarshal(data, &m) != nil || m.Identifier != w.id {
			continue // log lines and chat
		}
		return m.Message, nil
	}
}

// hideSecret takes a secret out of a message, as it's written and as a
// URL path escapes it (a password with a space or a % shows up encoded).
func hideSecret(msg, secret string) string {
	if secret == "" {
		return msg
	}
	for _, form := range []string{secret, url.PathEscape(secret), url.QueryEscape(secret)} {
		msg = strings.ReplaceAll(msg, form, "…")
	}
	return msg
}
