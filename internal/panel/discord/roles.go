package discord

import (
	"context"
	"net/http"
	"slices"
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

// Sync gives the Customer role to every member who should have it and
// takes it from every member who shouldn't. It reports false, doing
// nothing, if another instance is already doing it.
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

	want, err := b.customers(ctx)
	if err != nil {
		return true, err
	}
	added, removed := 0, 0
	for after := "0"; ; {
		var page []member
		if err := b.call(ctx, http.MethodGet, "/guilds/"+b.GuildID+"/members?limit=1000&after="+after, nil, &page, ""); err != nil {
			return true, err
		}
		for _, m := range page {
			if m.User.Bot {
				continue
			}
			has := slices.Contains(m.Roles, b.CustomerRole)
			switch {
			case want[m.User.ID] && !has:
				if err := b.setCustomer(ctx, m.User.ID, true); err != nil {
					b.log().Warn("discord: adding the Customer role failed", "user", m.User.ID, "err", err)
					continue
				}
				added++
			case !want[m.User.ID] && has:
				if err := b.setCustomer(ctx, m.User.ID, false); err != nil {
					b.log().Warn("discord: removing the Customer role failed", "user", m.User.ID, "err", err)
					continue
				}
				removed++
			}
		}
		if len(page) < membersPage {
			break
		}
		after = page[len(page)-1].User.ID
	}
	if added+removed > 0 {
		b.log().Info("discord: customer roles updated", "added", added, "removed", removed)
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
// the server, or ask with /link. It reports whether they're a customer.
func (b *Bot) checkMember(ctx context.Context, userID string, roles []string) (bool, error) {
	want, err := b.customers(ctx)
	if err != nil {
		return false, err
	}
	is := want[userID]
	if has := slices.Contains(roles, b.CustomerRole); is != has {
		return is, b.setCustomer(ctx, userID, is)
	}
	return is, nil
}
