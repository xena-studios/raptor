package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xena-studios/raptor/internal/wings/rcon"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// errNoPlayersRCON is a server whose egg's RCON has no Players command set.
var errNoPlayersRCON = errors.New("this server's game has no player commands over RCON")

// rconOnline lists who's on: the game's list command, parsed.
func rconOnline(ctx context.Context, t server.RCONTarget) ([]rcon.Player, error) {
	if t.Game == "" {
		return nil, errNoPlayersRCON
	}
	c, err := rcon.Dial(ctx, t.Protocol, t.Addr, t.Password)
	if err != nil {
		return nil, rconError(err)
	}
	defer func() { _ = c.Close() }()
	out, err := c.Exec(ctx, rcon.ListCommand(t.Game))
	if err != nil {
		return nil, rconError(err)
	}
	return rcon.ParsePlayers(t.Game, out)
}

// rconManage runs an op's commands, built from the game's templates with
// its arguments checked: nothing a user sends reaches the console except
// a checked ID and one plain line of text.
func rconManage(ctx context.Context, t server.RCONTarget, o rcon.Op) (string, error) {
	if t.Game == "" {
		return "", errNoPlayersRCON
	}
	cmds, err := rcon.Commands(t.Game, o)
	if err != nil {
		return "", err
	}
	c, err := rcon.Dial(ctx, t.Protocol, t.Addr, t.Password)
	if err != nil {
		return "", rconError(err)
	}
	defer func() { _ = c.Close() }()
	var replies []string
	for _, cmd := range cmds {
		out, err := c.Exec(ctx, cmd)
		if err != nil {
			return "", rconError(err)
		}
		if rcon.Refused(out) {
			return "", fmt.Errorf("the game doesn't take %s", strings.Fields(cmd)[0])
		}
		if out = strings.TrimSpace(out); out != "" {
			replies = append(replies, out)
		}
	}
	return strings.Join(replies, "\n"), nil
}

// rconError says what to do about a connection that failed.
func rconError(err error) error {
	if errors.Is(err, rcon.ErrAuth) {
		return errors.New("the game refused the RCON password: if it was changed in Startup, restart the server")
	}
	return fmt.Errorf("couldn't reach the game's RCON (is the server done starting?): %w", err)
}
