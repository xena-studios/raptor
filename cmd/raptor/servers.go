package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/term"
	"google.golang.org/protobuf/encoding/protojson"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1/localv1connect"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/localapi"
)

// Short IDs shown by ps: the random end of the UUIDv7 (its start is a
// timestamp that servers created together share). Wings accepts them.
const shortID = 8

// parseArgs parses flags anywhere among the arguments (`raptor logs srv -f`
// as well as `raptor logs -f srv`) and returns the positional ones.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// dial parses a command's flags (adding -socket) and returns the client and
// the positional arguments.
func dial(name string, args []string, setup func(*flag.FlagSet)) (localv1connect.LocalServiceClient, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	socket := fs.String("socket", config.Default().Paths.Socket, "Wings socket")
	if setup != nil {
		setup(fs)
	}
	pos, err := parseArgs(fs, args)
	if err != nil {
		return nil, nil, err
	}
	return localapi.Dial(*socket), pos, nil
}

// oneServer requires exactly one server argument.
func oneServer(name string, pos []string) (string, error) {
	if len(pos) != 1 {
		return "", fmt.Errorf("usage: raptor %s <server> (an ID, short ID, or name; see raptor ps)", name)
	}
	return pos[0], nil
}

// rpcErr turns a local API error into a message for people.
func rpcErr(err error) error {
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return err
	}
	switch ce.Code() {
	case connect.CodeUnavailable:
		if strings.Contains(ce.Message(), "dial unix") || strings.Contains(ce.Message(), "connect:") {
			return errors.New("can't reach Wings (is raptor-wings running, and are you root or in the raptor group?)")
		}
	case connect.CodeCanceled:
		return context.Canceled
	}
	return errors.New(ce.Message())
}

func ps(ctx context.Context, args []string) error {
	var asJSON *bool
	c, _, err := dial("ps", args, func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") })
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := c.ListServers(ctx, &localv1.ListServersRequest{})
	if err != nil {
		return rpcErr(err)
	}
	if *asJSON {
		b, err := protojson.Marshal(res)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	if len(res.GetServers()) == 0 {
		fmt.Println("no servers yet")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tSTATE\tCPU\tMEMORY\tDISK\tADDRESS\tUPTIME")
	for _, s := range res.GetServers() {
		id := s.GetId()
		if len(id) > shortID {
			id = id[len(id)-shortID:]
		}
		state := s.GetState()
		if s.GetInstallError() != "" && state == "install_failed" {
			state += " (" + truncate(s.GetInstallError(), 40) + ")"
		}
		cpu, mem, uptime := "-", "-", "-"
		switch s.GetState() {
		case "starting", "running", "stopping":
			cpu = fmt.Sprintf("%.1f%%", s.GetCpuPercent())
			mem = human(s.GetMemoryBytes()) + " / " + human(s.GetMemoryLimitBytes())
		}
		if s.GetRunningSince() != nil {
			uptime = duration(time.Since(s.GetRunningSince().AsTime()))
		}
		disk := human(s.GetDiskBytes())
		if s.GetDiskLimitBytes() > 0 {
			disk += " / " + human(s.GetDiskLimitBytes())
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", id, s.GetName(), state, cpu, mem, disk, s.GetAddress(), uptime)
	}
	return w.Flush()
}

var powerVerbs = map[string]struct {
	action localv1.PowerAction
	doing  string
}{
	"start":   {localv1.PowerAction_POWER_ACTION_START, "starting"},
	"stop":    {localv1.PowerAction_POWER_ACTION_STOP, "stopping"},
	"restart": {localv1.PowerAction_POWER_ACTION_RESTART, "restarting"},
	"kill":    {localv1.PowerAction_POWER_ACTION_KILL, "killing"},
}

func power(ctx context.Context, verb string, args []string) error {
	c, pos, err := dial(verb, args, nil)
	if err != nil {
		return err
	}
	ref, err := oneServer(verb, pos)
	if err != nil {
		return err
	}
	v := powerVerbs[verb]
	// A stop waits for the server's stop timeout (at most 10 minutes).
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	fmt.Fprintf(os.Stderr, "%s %s…\n", v.doing, ref)
	res, err := c.Power(ctx, &localv1.PowerRequest{Server: ref, Action: v.action})
	if err != nil {
		return rpcErr(err)
	}
	fmt.Printf("%s: %s\n", ref, res.GetState())
	return nil
}

func logs(ctx context.Context, args []string) error {
	var lines *int
	var follow, stamps *bool
	c, pos, err := dial("logs", args, func(fs *flag.FlagSet) {
		lines = fs.Int("n", 100, "lines of history (-1 = all)")
		follow = fs.Bool("f", false, "follow new output")
		stamps = fs.Bool("t", false, "show timestamps")
	})
	if err != nil {
		return err
	}
	ref, err := oneServer("logs", pos)
	if err != nil {
		return err
	}
	n := int32(max(min(*lines, 1<<30), -1)) //nolint:gosec // clamped
	if n == 0 {
		return nil
	}
	st, err := c.TailLogs(ctx, &localv1.TailLogsRequest{Server: ref, Lines: n, Follow: *follow})
	if err != nil {
		return rpcErr(err)
	}
	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()
	for st.Receive() {
		m := st.Msg()
		if *stamps && m.GetTime() != nil {
			_, _ = out.WriteString(m.GetTime().AsTime().Local().Format(time.DateTime) + " ")
		}
		_, _ = out.WriteString(m.GetText() + "\n")
		if *follow {
			_ = out.Flush()
		}
	}
	if err := st.Err(); err != nil && ctx.Err() == nil {
		return rpcErr(err)
	}
	return nil
}

// pipeLinger is how long the console keeps printing after piped commands.
const pipeLinger = 2 * time.Second

// console streams a server's console. Lines typed (or piped) in are sent as
// commands, which needs root. Ctrl-C or Ctrl-D detaches; the server keeps
// running. With piped input it detaches shortly after the last command.
func console(ctx context.Context, args []string) error {
	c, pos, err := dial("console", args, nil)
	if err != nil {
		return err
	}
	ref, err := oneServer("console", pos)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st, err := c.StreamConsole(ctx, &localv1.StreamConsoleRequest{Server: ref})
	if err != nil {
		return rpcErr(err)
	}
	defer func() { _ = st.Close() }()

	tty := isTerminal(os.Stdin)
	if tty {
		note := "type a command and press Enter to send it"
		if os.Geteuid() != 0 {
			note = "read-only: sending commands needs root"
		}
		fmt.Fprintf(os.Stderr, "[raptor] console of %s; %s. Ctrl-C detaches, the server keeps running.\n", ref, note)
	}

	sendErr := make(chan error, 1)
	go func() {
		err := sendLines(ctx, c, ref, os.Stdin, tty)
		sendErr <- err
		if !tty && err == nil {
			// Piped commands: show what they printed before detaching.
			select {
			case <-time.After(pipeLinger):
			case <-ctx.Done():
			}
		}
		cancel() // end of input (Ctrl-D) detaches
	}()

	for st.Receive() {
		fmt.Println(st.Msg().GetText())
	}
	if err := st.Err(); err != nil && ctx.Err() == nil {
		return rpcErr(err)
	}
	select {
	case err := <-sendErr:
		return err
	default:
		return nil // the server ended the stream (e.g. it was deleted)
	}
}

// sendLines sends each line read from r as a console command. On a
// terminal, errors are shown and it carries on; otherwise the first error
// ends it.
func sendLines(ctx context.Context, c localv1connect.LocalServiceClient, ref string, r io.Reader, tty bool) error {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		cmd := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		_, err := c.SendCommand(ctx, &localv1.SendCommandRequest{Server: ref, Command: cmd})
		if err == nil {
			continue
		}
		if !tty || ctx.Err() != nil {
			return rpcErr(err)
		}
		fmt.Fprintln(os.Stderr, "[raptor] not sent:", rpcErr(err))
	}
	return sc.Err()
}

func isTerminal(f *os.File) bool { return term.IsTerminal(int(f.Fd())) } //nolint:gosec // file descriptors fit in int

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// duration formats an uptime compactly: 45s, 12m, 3h12m, 5d3h.
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}
