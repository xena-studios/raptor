package discord

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
)

// InteractionsPath is where Discord sends slash commands and button
// presses (the Developer Portal's Interactions Endpoint URL, on the API's
// origin).
const InteractionsPath = "/discord/interactions"

// Interaction types, and the answers to them.
const (
	interactionPing         = 1
	interactionCommand      = 2
	interactionComponent    = 3
	interactionAutocomplete = 4
	interactionModal        = 5

	answerPong         = 1
	answerMessage      = 4
	answerDeferred     = 5
	answerAutocomplete = 8
	answerModal        = 9

	// flagEphemeral makes a reply only the person who asked can see.
	flagEphemeral = 1 << 6
	// maxSkew is how old a signed interaction may be: Discord sends them
	// at once, so an old one is a replay.
	maxSkew = 5 * time.Minute
)

type interaction struct {
	Type          int    `json:"type"`
	ApplicationID string `json:"application_id"`
	Token         string `json:"token"`
	GuildID       string `json:"guild_id"`
	ChannelID     string `json:"channel_id"`
	Data          struct {
		Name     string   `json:"name"`
		CustomID string   `json:"custom_id"`
		Options  []option `json:"options"`
		Resolved struct {
			Users map[string]user `json:"users"`
		} `json:"resolved"`
		// Components are a submitted form's fields.
		Components []formField `json:"components"`
	} `json:"data"`
	// Member is who it's from, in a server; User, in a direct message.
	Member *member `json:"member"`
	User   *user   `json:"user"`
}

// formField is a submitted form's component: a row or label holding a text
// input, or the input itself.
type formField struct {
	CustomID   string      `json:"custom_id"`
	Value      string      `json:"value"`
	Components []formField `json:"components"`
	Component  *formField  `json:"component"`
}

// value is a submitted form's field ("" if it's missing).
func (in *interaction) value(id string) string {
	var find func([]formField) string
	find = func(fs []formField) string {
		for _, f := range fs {
			if f.CustomID == id {
				return f.Value
			}
			if f.Component != nil && f.Component.CustomID == id {
				return f.Component.Value
			}
			if v := find(f.Components); v != "" {
				return v
			}
		}
		return ""
	}
	return find(in.Data.Components)
}

type option struct {
	Name    string          `json:"name"`
	Value   json.RawMessage `json:"value"`
	Focused bool            `json:"focused"`
}

// from is who sent the interaction, and their roles on the server.
func (in *interaction) from() (user, []string) {
	if in.Member != nil {
		return in.Member.User, in.Member.Roles
	}
	if in.User != nil {
		return *in.User, nil
	}
	return user{}, nil
}

// option is a string option's value ("" if it's missing).
func (in *interaction) option(name string) string {
	for _, o := range in.Data.Options {
		if o.Name == name {
			var s string
			_ = json.Unmarshal(o.Value, &s)
			return s
		}
	}
	return ""
}

type answer struct {
	Type int `json:"type"`
	Data any `json:"data,omitempty"`
}

type message struct {
	Content    string      `json:"content,omitempty"`
	Embeds     []embed     `json:"embeds,omitempty"`
	Components []component `json:"components,omitempty"`
	Flags      int         `json:"flags,omitempty"`
	// AllowedMentions is empty unless a message means to ping someone (a
	// ticket pings its opener and Support); nothing else ever does.
	AllowedMentions mentions `json:"allowed_mentions"`
}

type mentions struct {
	Parse []string `json:"parse"`
	Users []string `json:"users,omitempty"`
	Roles []string `json:"roles,omitempty"`
}

type embed struct {
	Title       string  `json:"title,omitempty"`
	URL         string  `json:"url,omitempty"`
	Description string  `json:"description,omitempty"`
	Color       int     `json:"color,omitempty"`
	Fields      []field `json:"fields,omitempty"`
	Footer      *footer `json:"footer,omitempty"`
}

type field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type footer struct {
	Text string `json:"text"`
}

// brandColor is DESIGN.md's red, for embeds.
const brandColor = 0xF05653

// Components: a row of buttons.
type component struct {
	Type       int      `json:"type"`
	Components []button `json:"components"`
}

type button struct {
	Type     int    `json:"type"`
	Style    int    `json:"style"`
	Label    string `json:"label"`
	Emoji    *emoji `json:"emoji,omitempty"`
	CustomID string `json:"custom_id,omitempty"`
	URL      string `json:"url,omitempty"`
}

type emoji struct {
	Name string `json:"name"`
}

const (
	buttonPrimary   = 1
	buttonSecondary = 2
	buttonLink      = 5
)

func row(buttons ...button) component {
	for i := range buttons {
		buttons[i].Type = 2
	}
	return component{Type: 1, Components: buttons}
}

// reply is a message answer; private ones only the asker sees.
func reply(m message, private bool) answer {
	if private {
		m.Flags |= flagEphemeral
	}
	m.AllowedMentions.Parse = []string{}
	return answer{Type: answerMessage, Data: m}
}

func say(text string) answer { return reply(message{Content: text}, true) }

// ServeHTTP answers Discord's interactions. Each is signed with the
// application's key; anything else is refused, which Discord also checks
// when the endpoint is set.
func (b *Bot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sig, err := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
	ts := r.Header.Get("X-Signature-Timestamp")
	if err != nil || len(sig) != ed25519.SignatureSize || len(b.PublicKey) != ed25519.PublicKeySize ||
		!ed25519.Verify(b.PublicKey, append([]byte(ts), body...), sig) {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}
	if sec, err := strconv.ParseInt(ts, 10, 64); err != nil || b.now().Sub(time.Unix(sec, 0)).Abs() > maxSkew {
		http.Error(w, "stale request", http.StatusUnauthorized)
		return
	}
	var in interaction
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Discord waits 3 seconds for an answer.
	ctx, cancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
	defer cancel()
	out := b.handle(ctx, &in)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (b *Bot) handle(ctx context.Context, in *interaction) answer {
	switch in.Type {
	case interactionPing:
		return answer{Type: answerPong}
	case interactionAutocomplete:
		if in.Data.Name == "docs" {
			return answer{Type: answerAutocomplete, Data: map[string]any{"choices": b.docsChoices(ctx, in.option("page"))}}
		}
		return answer{Type: answerAutocomplete, Data: map[string]any{"choices": []any{}}}
	case interactionCommand:
		switch in.Data.Name {
		case "link":
			return b.link(ctx, in)
		case "docs":
			return b.docsCommand(ctx, in)
		case "whois":
			return b.whois(ctx, in)
		}
	case interactionComponent:
		switch in.Data.CustomID {
		case linkButton:
			return b.link(ctx, in)
		case ticketButton:
			return b.ticketForm(ctx, in)
		case closeButton:
			return b.closeTicket(ctx, in)
		}
	case interactionModal:
		if in.Data.CustomID == ticketForm {
			return b.openTicket(ctx, in)
		}
	}
	return say("That isn't something Raptor Bot knows how to do (yet).")
}
