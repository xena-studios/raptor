// Package mojang looks Minecraft players up by name (#422): their UUID,
// for editing a stopped server's whitelist, ops, and bans, where the game
// isn't running to look them up itself. Answers are cached (webapi).
package mojang

import (
	"context"
	"crypto/md5" //nolint:gosec // Minecraft's offline UUIDs are MD5-based (UUID v3)
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/panel/webapi"
)

// DefaultBase is Mojang's account API.
const DefaultBase = "https://api.mojang.com"

// Errors.
var (
	ErrNotFound    = errors.New("no Minecraft account has that name")
	ErrUnavailable = errors.New("mojang isn't answering right now; try again in a minute")
)

var name = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)

// ValidName reports whether s can be a Minecraft name.
func ValidName(s string) bool { return name.MatchString(s) }

// Client calls Mojang.
type Client struct {
	Base      string
	HTTP      *http.Client
	UserAgent string
	Now       func() time.Time

	once sync.Once
	api  *webapi.Client
}

// Player is an account: its name as Mojang has it, and its UUID.
type Player struct {
	Name string
	UUID string
}

// Lookup is the account with a name.
func (c *Client) Lookup(ctx context.Context, player string) (Player, error) {
	if !ValidName(player) {
		return Player{}, ErrNotFound
	}
	c.once.Do(func() {
		base := c.Base
		if base == "" {
			base = DefaultBase
		}
		c.api = &webapi.Client{Base: base, HTTP: c.HTTP, UserAgent: c.UserAgent, Now: c.Now, NotFound: ErrNotFound, Unavailable: ErrUnavailable}
	})
	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.api.Do(ctx, "/users/profiles/minecraft/"+player, nil, nil, &out); err != nil {
		return Player{}, err
	}
	if len(out.ID) != 32 {
		return Player{}, ErrNotFound
	}
	return Player{Name: out.Name, UUID: dashed(out.ID)}, nil
}

func dashed(h string) string {
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// OfflineUUID is the UUID an offline-mode server gives a name: a version 3
// UUID of "OfflinePlayer:<name>", as Minecraft makes it.
func OfflineUUID(player string) string {
	sum := md5.Sum([]byte("OfflinePlayer:" + player)) //nolint:gosec // see the import
	sum[6] = sum[6]&0x0f | 0x30
	sum[8] = sum[8]&0x3f | 0x80
	return dashed(fmt.Sprintf("%x", sum))
}
