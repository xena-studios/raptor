package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

const dbUsage = `usage: raptor db <command>

  ps [-json]    the node's database instances, with their state and usage`

func dbCmd(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "ps" {
		return dbPs(ctx, args[1:])
	}
	fmt.Fprintln(os.Stderr, dbUsage)
	return errUsage
}

// dbPs is `raptor db ps`: the database instances (docs/DATABASES.md), as
// Wings has them.
func dbPs(ctx context.Context, args []string) error {
	var asJSON *bool
	c, _, err := dial("db ps", args, func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") })
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := c.ListDatabaseInstances(ctx, &localv1.ListDatabaseInstancesRequest{})
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
	return printInstances(os.Stdout, res.GetInstances())
}

var engineNames = map[string]string{
	"mariadb": "MariaDB", "mysql": "MySQL", "postgres": "PostgreSQL", "redis": "Redis", "mongodb": "MongoDB",
}

func printInstances(out io.Writer, list []*localv1.DatabaseInstanceInfo) error {
	if len(list) == 0 {
		_, err := fmt.Fprintln(out, "no database instances on this node")
		return err
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tENGINE\tSTATE\tMEMORY\tDISK\tCONNECTIONS\tDATABASES\tNETWORKS")
	for _, in := range list {
		id := in.GetId()
		if len(id) > shortID {
			id = id[len(id)-shortID:]
		}
		engine := engineNames[in.GetEngine()]
		if engine == "" {
			engine = in.GetEngine()
		}
		state := in.GetState()
		if in.GetError() != "" && state == "failed" {
			state += " (" + truncate(in.GetError(), 40) + ")"
		}
		mem, disk, conns := "-", "-", "-"
		if in.GetSampledAt() != nil {
			mem = human(in.GetMemoryBytes()) + " / " + human(in.GetMemoryLimitBytes())
			disk = human(in.GetDiskBytes())
			conns = fmt.Sprintf("%d / %d", in.GetConnections(), in.GetMaxConnections())
		}
		if in.GetDiskLimitBytes() > 0 {
			disk += " / " + human(in.GetDiskLimitBytes())
		}
		nets := make([]string, 0, len(in.GetNetworks()))
		for _, n := range in.GetNetworks() {
			name := n.GetName()
			if name == "" {
				name = n.GetDockerName()
			}
			if n.GetAddress() != "" {
				name += " (" + n.GetAddress() + ")"
			}
			nets = append(nets, name)
		}
		if len(nets) == 0 {
			nets = []string{"server network"}
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s %s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			id, in.GetName(), engine, in.GetVersion(), state, mem, disk, conns, in.GetDatabases(), strings.Join(nets, ", "))
	}
	return w.Flush()
}
