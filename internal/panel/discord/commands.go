package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// linkButton is the custom ID of the "Link my account" button.
const linkButton = "raptor:link"

// permModerateMembers hides /whois from everyone but moderators in
// Discord's menus; the Panel also checks the asker is Raptor staff.
const permModerateMembers = "1099511627776"

// commands are the slash commands, as Discord's API takes them.
var commands = []map[string]any{
	{
		"name":        "link",
		"description": "Link your Raptor account, or see how it's linked",
		"contexts":    []int{0},
	},
	{
		"name":        "docs",
		"description": "Find a page in Raptor's guide",
		"contexts":    []int{0},
		"options": []map[string]any{{
			"type": 3, "name": "page", "description": "What you're looking for", "required": true, "autocomplete": true,
		}},
	},
	{
		"name":                       "whois",
		"description":                "Staff: the Raptor account a member has linked",
		"contexts":                   []int{0},
		"default_member_permissions": permModerateMembers,
		"options": []map[string]any{{
			"type": 6, "name": "member", "description": "Who to look up", "required": true,
		}},
	},
}

// Register makes the server's slash commands these (Online does it, so
// they change with the Panel's version).
func (b *Bot) Register(ctx context.Context) error {
	app := b.AppID
	if app == "" {
		var a struct {
			ID string `json:"id"`
		}
		if err := b.call(ctx, http.MethodGet, "/applications/@me", nil, &a, ""); err != nil {
			return err
		}
		app = a.ID
	}
	return b.call(ctx, http.MethodPut, "/applications/"+app+"/guilds/"+b.GuildID+"/commands", commands, nil, "")
}

func (b *Bot) settingsURL() string {
	return strings.TrimRight(b.AppURL, "/") + "/settings?connect=discord"
}

// link answers /link and the "Link my account" button: how to link, or
// what's linked, with the Customer role brought up to date on the spot.
func (b *Bot) link(ctx context.Context, in *interaction) answer {
	who, roles := in.from()
	acct, err := store.New(b.DB).DiscordUser(ctx, who.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return reply(message{
			Embeds: []embed{{
				Title: "Link your Raptor account",
				Description: "Open Raptor's settings and connect Discord, with this Discord account. " +
					"It can sign you in to Raptor after that, and owners and admins of an org with a plan get the **Customer** role here within a minute.",
				Color: brandColor,
			}},
			Components: []component{row(button{Style: buttonLink, Label: "Connect Discord in Raptor", URL: b.settingsURL()})},
		}, true)
	}
	if err != nil {
		b.log().Error("discord: /link failed", "err", err)
		return say("Something went wrong on Raptor's side. Try again in a minute.")
	}
	customer, err := b.checkMember(ctx, who.ID, roles)
	if err != nil {
		b.log().Warn("discord: updating a member's role failed", "user", who.ID, "err", err)
	}
	text := fmt.Sprintf("This Discord account is linked to **%s** on Raptor.\n\n", acct.Email)
	if customer {
		text += "You have the **Customer** role."
	} else {
		text += "No **Customer** role: it's for owners and admins of an org with a plan, the free month included. It's added within a minute of that changing."
	}
	text += "\n\nTo unlink it, disconnect Discord in Raptor's settings."
	return reply(message{
		Embeds:     []embed{{Description: text, Color: brandColor}},
		Components: []component{row(button{Style: buttonLink, Label: "Raptor settings", URL: b.settingsURL()})},
	}, true)
}

// whois answers /whois: the Raptor account a member linked, their orgs,
// plans, and nodes. Only Raptor staff, by their own linked accounts, can
// ask; the answer is only theirs to see, and goes in the staff audit log.
func (b *Bot) whois(ctx context.Context, in *interaction) answer {
	asker, _ := in.from()
	q := store.New(b.DB)
	staff, err := q.DiscordUser(ctx, asker.ID)
	if err != nil {
		return say("Only Raptor staff can use /whois, with their Discord linked to their Raptor account.")
	}
	role, err := q.StaffRole(ctx, staff.ID)
	if err != nil {
		return say("Only Raptor staff can use /whois, with their Discord linked to their Raptor account.")
	}
	var target string
	for _, o := range in.Data.Options {
		if o.Name == "member" {
			_ = json.Unmarshal(o.Value, &target)
		}
	}
	name := target
	if u, ok := in.Data.Resolved.Users[target]; ok {
		name = displayName(u)
	}
	acct, err := q.DiscordUser(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return say(fmt.Sprintf("%s hasn't linked a Raptor account.", name))
	}
	if err != nil {
		b.log().Error("discord: /whois failed", "err", err)
		return say("Something went wrong on Raptor's side. Try again in a minute.")
	}
	orgs, err := q.DiscordUserOrgs(ctx, acct.ID)
	if err != nil {
		b.log().Error("discord: /whois failed", "err", err)
		return say("Something went wrong on Raptor's side. Try again in a minute.")
	}
	meta, _ := json.Marshal(map[string]any{"discord_user": target, "via": "discord"})
	if err := q.AddStaffAudit(ctx, store.AddStaffAuditParams{
		StaffID: staff.ID, StaffEmail: staff.Email, Action: "user.whois", TargetType: "user", TargetID: uuidString(acct.ID), Meta: meta,
	}); err != nil {
		b.log().Error("discord: recording a /whois failed", "err", err)
		return say("Something went wrong on Raptor's side. Try again in a minute.")
	}

	e := embed{
		Title: name,
		URL:   strings.TrimRight(b.AppURL, "/") + "/admin/users/" + uuidString(acct.ID),
		Description: fmt.Sprintf("**%s**\nRaptor account since %s · Discord linked %s",
			acct.Email, day(acct.CreatedAt), day(acct.LinkedAt)),
		Color:  brandColor,
		Footer: &footer{Text: "Looked up by " + staff.Email + " (" + role + "); this is in the staff audit log."},
	}
	e.Fields = b.orgFields(orgs)
	return reply(message{Embeds: []embed{e}}, true)
}

// orgFields describe someone's orgs: role, plan, and nodes, for /whois and
// a ticket's first message.
func (b *Bot) orgFields(orgs []store.DiscordUserOrgsRow) []field {
	if len(orgs) == 0 {
		return []field{{Name: "Orgs", Value: "None"}}
	}
	var out []field
	for i, o := range orgs {
		if i == 10 {
			out = append(out, field{Name: "…", Value: fmt.Sprintf("and %d more orgs", len(orgs)-10)})
			break
		}
		lines := []string{fmt.Sprintf("%s · %s", titleCase(o.Role), plan(o.Status, b.Billing))}
		if o.Suspended {
			lines = append(lines, "**Suspended**")
		}
		nodes := fmt.Sprintf("%d node%s", o.Nodes, plural(int(o.Nodes)))
		if o.Versions != "" {
			nodes += " · Wings " + o.Versions
		}
		if o.LastSeenAt.Valid {
			nodes += " · seen " + ago(b.now().Sub(o.LastSeenAt.Time))
		}
		lines = append(lines, nodes)
		out = append(out, field{Name: o.Name, Value: strings.Join(lines, "\n")})
	}
	return out
}

func displayName(u user) string {
	if u.GlobalName != "" {
		return u.GlobalName + " (@" + u.Username + ")"
	}
	return "@" + u.Username
}

func plan(status string, billingOn bool) string {
	switch {
	case !billingOn:
		return "billing off"
	case status == "":
		return "no plan"
	case status == "trialing":
		return "free month"
	case status == "past_due":
		return "payment failed"
	case billing.Live(status):
		return "paid"
	}
	return "plan " + status
}

func uuidString(id pgtype.UUID) string {
	b := id.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func day(t pgtype.Timestamptz) string {
	if !t.Valid {
		return "never"
	}
	return t.Time.UTC().Format("Jan 2, 2006")
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
