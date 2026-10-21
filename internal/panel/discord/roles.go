package discord

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/store"
)

const (
	// rolesLock is the advisory lock a pass over the roles holds, so one
	// instance makes the changes.
	rolesLock = 0x52_44_49_53_52_4c // "RDISRL"
	// settle is how long Changed waits for more changes before a pass, so
	// a burst of them makes one.
	settle = 2 * time.Second
	// membersPage is the most members Discord lists at once.
	membersPage = 1000
)

// Changed asks for the Customer role to be checked soon: someone linked or
// unlinked Discord, joined or left an org, or an org's plan changed. Safe
// on a nil Bot.
func (b *Bot) Changed() {
	if b == nil {
		return
	}
	b.init()
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

// Run keeps the Customer role right: soon after a change on this instance,
// and every interval in case one was missed (a change made on another
// instance, a member who came back, a role someone edited by hand).
func (b *Bot) Run(ctx context.Context, interval time.Duration) {
	b.init()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-b.kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(settle):
			}
		}
		// Another instance's pass may have started before this change;
		// wait for it to finish rather than skip.
		for range 12 {
			ran, err := b.Sync(ctx)
			if err != nil && ctx.Err() == nil {
				b.log().Error("discord: updating roles failed", "err", err)
			}
			if ran || ctx.Err() != nil {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// customers are the Discord accounts that should have the Customer role.
func (b *Bot) customers(ctx context.Context) (map[string]bool, error) {
	ids, err := store.New(b.DB).DiscordCustomers(ctx, store.DiscordCustomersParams{Billing: b.Billing, Live: billing.LiveStatuses})
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

type user struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Bot        bool   `json:"bot"`
}

type member struct {
	User  user     `json:"user"`
	Roles []string `json:"roles"`
}

// Plan is what a pass over the roles would change.
type Plan struct {
	Add, Remove []user
}

// plan compares the server's members with who should be a customer.
func (b *Bot) plan(ctx context.Context) (Plan, error) {
	var p Plan
	want, err := b.customers(ctx)
	if err != nil {
		return p, err
	}
	for after := "0"; ; {
		var page []member
		if err := b.call(ctx, http.MethodGet, "/guilds/"+b.GuildID+"/members?limit=1000&after="+after, nil, &page, ""); err != nil {
			return p, err
		}
		for _, m := range page {
			if m.User.Bot {
				continue
			}
			switch has := slices.Contains(m.Roles, b.CustomerRole); {
			case want[m.User.ID] && !has:
				p.Add = append(p.Add, m.User)
			case !want[m.User.ID] && has:
				p.Remove = append(p.Remove, m.User)
			}
		}
		if len(page) < membersPage {
			return p, nil
		}
		after = page[len(page)-1].User.ID
	}
}

// Preview is what a pass would change now, removals included whether or
// not they're on (panel discord roles preview).
func (b *Bot) Preview(ctx context.Context) (Plan, error) { return b.plan(ctx) }

// Enforce turns taking the Customer role away on or off (panel discord
// roles enforce). Until it's on, the bot only adds it, and posts who it
// would take it from to the staff log.
func (b *Bot) Enforce(ctx context.Context, on bool) error {
	return store.New(b.DB).SetDiscordRemoveRoles(ctx, on)
}

// Sync gives the Customer role to every member who should have it and,
// once removals are on, takes it from every member who shouldn't; until
// then it posts those to the staff log, once per change. It reports false,
// doing nothing, if another instance is already doing it.
func (b *Bot) Sync(ctx context.Context) (bool, error) {
	conn, err := b.DB.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(rolesLock)).Scan(&locked); err != nil || !locked {
		return false, err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(rolesLock))
	}()

	q := store.New(b.DB)
	settings, err := q.DiscordSettings(ctx)
	if err != nil {
		return true, err
	}
	p, err := b.plan(ctx)
	if err != nil {
		return true, err
	}
	apply := func(us []user, on bool) []user {
		var done []user
		for _, u := range us {
			if err := b.setCustomer(ctx, u.ID, on); err != nil {
				b.log().Warn("discord: changing the Customer role failed", "user", u.ID, "add", on, "err", err)
				continue
			}
			done = append(done, u)
		}
		return done
	}
	added := apply(p.Add, true)
	var removed []user
	if settings.RemoveRoles {
		removed = apply(p.Remove, false)
	} else {
		// Said once per change, not every pass.
		ids := make([]string, len(p.Remove))
		for i, u := range p.Remove {
			ids[i] = u.ID
		}
		if n, err := q.SetDiscordPreview(ctx, strings.Join(ids, ",")); err == nil && n > 0 && len(ids) > 0 {
			b.staffLog(ctx, fmt.Sprintf("**Customer role:** %s %s it but %s linked owners or admins of an org with a plan: %s\n"+
				"Raptor Bot leaves it until removals are on (`panel discord roles enforce on`).",
				count(len(ids), "member"), map[bool]string{true: "has", false: "have"}[len(ids) == 1],
				map[bool]string{true: "isn't one of the", false: "aren't"}[len(ids) == 1], mentionList(p.Remove)))
		}
	}
	if len(added)+len(removed) > 0 {
		b.log().Info("discord: customer roles updated", "added", len(added), "removed", len(removed))
		var lines []string
		if len(added) > 0 {
			lines = append(lines, "**Customer role added:** "+mentionList(added))
		}
		if len(removed) > 0 {
			lines = append(lines, "**Customer role removed:** "+mentionList(removed))
		}
		b.staffLog(ctx, strings.Join(lines, "\n"))
	}
	return true, nil
}

// setCustomer adds or removes a member's Customer role.
func (b *Bot) setCustomer(ctx context.Context, userID string, on bool) error {
	method, reason := http.MethodPut, "Linked to a Raptor account with a plan"
	if !on {
		method, reason = http.MethodDelete, "No longer linked to a Raptor account with a plan"
	}
	err := b.call(ctx, method, "/guilds/"+b.GuildID+"/members/"+userID+"/roles/"+b.CustomerRole, nil, nil, reason)
	if isUnknown(err) {
		return nil // they left the server
	}
	return err
}

// checkMember gives or takes one member's Customer role now: when they join
// the server, or ask with /link. It's only taken once removals are on. It
// reports whether they're a customer.
func (b *Bot) checkMember(ctx context.Context, userID string, roles []string) (bool, error) {
	want, err := b.customers(ctx)
	if err != nil {
		return false, err
	}
	is, has := want[userID], slices.Contains(roles, b.CustomerRole)
	if is == has {
		return is, nil
	}
	if !is {
		settings, err := store.New(b.DB).DiscordSettings(ctx)
		if err != nil || !settings.RemoveRoles {
			return is, err
		}
	}
	if err := b.setCustomer(ctx, userID, is); err != nil {
		return is, err
	}
	verb := "added"
	if !is {
		verb = "removed"
	}
	b.staffLog(ctx, fmt.Sprintf("**Customer role %s:** <@%s>", verb, userID))
	return is, nil
}

// staffLog posts to the staff log channel, if there is one, pinging nobody.
func (b *Bot) staffLog(ctx context.Context, text string) {
	if b.LogChannel == "" {
		return
	}
	if err := b.call(ctx, http.MethodPost, "/channels/"+b.LogChannel+"/messages", map[string]any{
		"content": clip(text, 2000), "allowed_mentions": mentions{Parse: []string{}},
	}, nil, ""); err != nil {
		b.log().Warn("discord: posting to the staff log failed", "err", err)
	}
}

// mentionList is members as mentions, up to 40, then how many more.
func mentionList(us []user) string {
	var parts []string
	for i, u := range us {
		if i == 40 {
			parts = append(parts, fmt.Sprintf("and %d more", len(us)-40))
			break
		}
		parts = append(parts, "<@"+u.ID+">")
	}
	return strings.Join(parts, ", ")
}

func count(n int, noun string) string {
	return fmt.Sprintf("%d %s%s", n, noun, plural(n))
}
