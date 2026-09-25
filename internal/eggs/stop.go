package eggs

import (
	"strings"
	"syscall"
)

// Stop is how a server is asked to stop: a console command or a signal.
type Stop struct {
	// Command is written to the server's stdin. Empty when Signal is used.
	Command string
	// Signal is sent to the container when Command is empty. Zero means the
	// egg defines no stop behavior and the container's own stop signal is used.
	Signal syscall.Signal
}

// ParseStop interprets an egg's config.stop value the way Pterodactyl does:
// values starting with "^" are signals, anything else is a console command.
// After removing one "^", "C" or "SIGINT" means SIGINT, "SIGTERM" and
// "SIGABRT" mean themselves, and anything else (including "^C", i.e. "^^C")
// means SIGKILL.
func ParseStop(v string) Stop {
	if v == "" {
		return Stop{}
	}
	if !strings.HasPrefix(v, "^") {
		return Stop{Command: v}
	}
	switch strings.ToUpper(strings.TrimPrefix(v, "^")) {
	case "C", "SIGINT":
		return Stop{Signal: syscall.SIGINT}
	case "SIGTERM":
		return Stop{Signal: syscall.SIGTERM}
	case "SIGABRT":
		return Stop{Signal: syscall.SIGABRT}
	default:
		return Stop{Signal: syscall.SIGKILL}
	}
}
