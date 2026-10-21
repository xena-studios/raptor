package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// Tickets are private threads in the channel with the "Open a ticket"
// button: the person who opened it, the bot, and Support (who can see every
// private thread there) are in it. Closing one locks and archives it, so
// it's still there to read.
const (
	ticketButton = "raptor:ticket"
	ticketForm   = "raptor:ticket-form"
	closeButton  = "raptor:ticket-close"

	// threadPrivate is a private thread; it archives after a week quiet.
	threadPrivate = 12
	archiveAfter  = 7 * 24 * 60
)

// archiveDelay is how long a closed ticket's thread stays open for the
// answer saying so to be posted.
var archiveDelay = 2 * time.Second

// supportCode is a support code from raptor doctor -upload (support.NewCode).
var supportCode = regexp.MustCompile(`^RPT-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

// PostTicketMessage posts the "Open a ticket" message, with its button, to
// a channel; or, given one of the bot's messages there, adds the button to
// it (panel discord post tickets).
func (b *Bot) PostTicketMessage(ctx context.Context, channelID, messageID string) error {
	buttons := []component{row(button{Style: buttonPrimary, Label: "Open a ticket", CustomID: ticketButton, Emoji: &emoji{Name: "🎫"}})}
	if messageID != "" {
		return b.call(ctx, http.MethodPatch, "/channels/"+channelID+"/messages/"+messageID, map[string]any{"components": buttons}, nil, "")
	}
	return b.call(ctx, http.MethodPost, "/channels/"+channelID+"/messages", map[string]any{
		"embeds": []embed{{
			Title: "Open a ticket",
			Description: "A private thread with Raptor's team, for your account, billing, a node that won't behave, or moving from Pterodactyl. " +
				"How-to questions are often answered faster in the docs: https://docs.raptorpanel.net\n\n" +
				"Got a support code from `sudo raptor doctor -upload`? Put it in the form, and we can see what your node sees.\n\n" +
				"Security vulnerabilities go to security@raptorpanel.net, never a ticket.",
			Color: brandColor,
		}},
		"components": buttons,
	}, nil, "")
}

// openTicketOf is someone's open ticket, if its thread is still open: one
// archived or deleted by hand is closed here too.
func (b *Bot) openTicketOf(ctx context.Context, userID string) (string, error) {
	q := store.New(b.DB)
	t, err := q.OpenDiscordTicket(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var ch struct {
		Meta struct {
			Archived bool `json:"archived"`
		} `json:"thread_metadata"`
	}
	err = b.call(ctx, http.MethodGet, "/channels/"+t.ThreadID, nil, &ch, "")
	switch {
	case isUnknown(err) || (err == nil && ch.Meta.Archived):
		_, err := q.CloseDiscordTicket(ctx, store.CloseDiscordTicketParams{ThreadID: t.ThreadID, ClosedBy: "discord"})
		return "", err
	case err != nil:
		return "", err
	}
	return t.ThreadID, nil
}

// ticketForm answers the "Open a ticket" button with the form, unless
// they've a ticket open already.
func (b *Bot) ticketForm(ctx context.Context, in *interaction) answer {
	who, _ := in.from()
	open, err := b.openTicketOf(ctx, who.ID)
	if err != nil {
		b.log().Error("discord: checking for an open ticket failed", "err", err)
		return say("Something went wrong on Raptor's side. Try again in a minute.")
	}
	if open != "" {
		return say(fmt.Sprintf("You've a ticket open already: <#%s>. Add to it there.", open))
	}
	input := func(id, label, placeholder string, style, maxLen int, required bool) map[string]any {
		return map[string]any{"type": 1, "components": []map[string]any{{
			"type": 4, "custom_id": id, "label": label, "placeholder": placeholder,
			"style": style, "max_length": maxLen, "required": required,
		}}}
	}
	return answer{Type: answerModal, Data: map[string]any{
		"custom_id": ticketForm,
		"title":     "Open a ticket",
		"components": []map[string]any{
			input("topic", "What's it about?", "Backups fail on my node", 1, 80, true),
			input("details", "What's happening?", "What you did, what you expected, and what happened instead.", 2, 1500, true),
			input("code", "Support code (optional)", "RPT-XXXX-XXXX, from sudo raptor doctor -upload", 1, 13, false),
		},
	}}
}

// openTicket answers the form: it makes the thread, adds them, and tells
// Support. That's several requests to Discord, so the answer is deferred
// and filled in when it's done.
func (b *Bot) openTicket(ctx context.Context, in *interaction) answer {
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		text := b.makeTicket(ctx, in)
		if err := b.call(ctx, http.MethodPatch, "/webhooks/"+in.ApplicationID+"/"+in.Token+"/messages/@original",
			map[string]any{"content": text, "allowed_mentions": mentions{Parse: []string{}}}, nil, ""); err != nil {
			b.log().Warn("discord: answering a ticket form failed", "err", err)
		}
	}()
	return answer{Type: answerDeferred, Data: map[string]any{"flags": flagEphemeral}}
}

// makeTicket opens the ticket and says how it went.
func (b *Bot) makeTicket(ctx context.Context, in *interaction) string {
	const failed = "Opening the ticket failed on Raptor's side. Try again in a minute, or email support@raptorpanel.net."
	who, _ := in.from()
	topic := strings.TrimSpace(in.value("topic"))
	details := strings.TrimSpace(in.value("details"))
	code := strings.ToUpper(strings.TrimSpace(in.value("code")))
	if open, err := b.openTicketOf(ctx, who.ID); err != nil {
		b.log().Error("discord: checking for an open ticket failed", "err", err)
		return failed
	} else if open != "" {
		return fmt.Sprintf("You've a ticket open already: <#%s>. Add to it there.", open)
	}

	var thread struct {
		ID string `json:"id"`
	}
	name := clip(topic+" · "+who.Username, 100)
	if err := b.call(ctx, http.MethodPost, "/channels/"+in.ChannelID+"/threads", map[string]any{
		"name": name, "type": threadPrivate, "invitable": false, "auto_archive_duration": archiveAfter,
	}, &thread, "Ticket for "+who.Username); err != nil {
		b.log().Error("discord: making a ticket's thread failed", "err", err)
		return failed
	}
	q := store.New(b.DB)
	if err := q.AddDiscordTicket(ctx, store.AddDiscordTicketParams{ThreadID: thread.ID, UserID: who.ID, Topic: topic}); err != nil {
		// Two forms at once: the other one won.
		b.log().Warn("discord: recording a ticket failed", "err", err)
		_ = b.call(ctx, http.MethodDelete, "/channels/"+thread.ID, nil, nil, "Duplicate ticket")
		return failed
	}
	if err := b.call(ctx, http.MethodPut, "/channels/"+thread.ID+"/thread-members/"+who.ID, nil, nil, ""); err != nil {
		b.log().Error("discord: adding someone to their ticket failed", "err", err)
		return failed
	}

	first := embed{Title: clip(topic, 256), Description: clip(details, 4000), Color: brandColor}
	if code != "" {
		v := "`" + code + "`"
		if !supportCode.MatchString(code) {
			v += " (doesn't look like a support code: they're RPT-XXXX-XXXX)"
		}
		first.Fields = append(first.Fields, field{Name: "Support code", Value: v})
	}
	if acct, err := q.DiscordUser(ctx, who.ID); err == nil {
		first.Fields = append(first.Fields, field{Name: "Raptor account", Value: acct.Email})
		if orgs, err := q.DiscordUserOrgs(ctx, acct.ID); err == nil {
			first.Fields = append(first.Fields, b.orgFields(orgs)...)
		}
	} else {
		first.Fields = append(first.Fields, field{Name: "Raptor account", Value: "Not linked. Use /link to link it, so we can see your plan and nodes."})
	}
	ping := "<@" + who.ID + ">"
	m := mentions{Parse: []string{}, Users: []string{who.ID}}
	if b.SupportRole != "" {
		ping += " <@&" + b.SupportRole + ">"
		m.Roles = []string{b.SupportRole}
	}
	if err := b.call(ctx, http.MethodPost, "/channels/"+thread.ID+"/messages", map[string]any{
		"content":          ping + " Thanks. Someone from Raptor's team will answer here; anything you add helps (screenshots, what changed lately).",
		"embeds":           []embed{first},
		"components":       []component{row(button{Style: buttonSecondary, Label: "Close ticket", CustomID: closeButton})},
		"allowed_mentions": m,
	}, nil, ""); err != nil {
		b.log().Error("discord: posting a ticket's first message failed", "err", err)
	}
	return fmt.Sprintf("Your ticket is open: <#%s>.", thread.ID)
}

// closeTicket answers "Close ticket" in a ticket's thread: it says who
// closed it, then locks and archives the thread, which stays readable.
func (b *Bot) closeTicket(ctx context.Context, in *interaction) answer {
	who, _ := in.from()
	if _, err := store.New(b.DB).CloseDiscordTicket(ctx, store.CloseDiscordTicketParams{ThreadID: in.ChannelID, ClosedBy: who.ID}); err != nil {
		b.log().Error("discord: closing a ticket failed", "err", err)
		return say("Something went wrong on Raptor's side. Try again in a minute.")
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		// After the answer is posted: an archived thread takes no messages.
		time.Sleep(archiveDelay)
		if err := b.call(ctx, http.MethodPatch, "/channels/"+in.ChannelID, map[string]any{"archived": true, "locked": true}, nil, "Ticket closed by "+who.Username); err != nil {
			b.log().Warn("discord: archiving a ticket failed", "thread", in.ChannelID, "err", err)
		}
	}()
	return reply(message{Content: fmt.Sprintf("Closed by <@%s>. Need more help? Open a new ticket any time.", who.ID)}, false)
}
