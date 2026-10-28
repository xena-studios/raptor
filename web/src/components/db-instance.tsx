import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { cn } from "cn";
import { ArrowLeft, Database as DatabaseIcon } from "lucide-react";
import { useState } from "react";

import { DatabaseBrowser } from "@/components/db-browser";
import { ProcessesTab } from "@/components/db-browser/processes";
import { InstanceUsageCards } from "@/components/db-usage";
import { PasswordDialog, ReadOnlyUser } from "@/components/server-databases";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { type Database, engineName, isSQL, useInstances, useInstanceUsage } from "@/lib/databases";
import { dbCommand } from "@/lib/db-browser";
import { message } from "@/lib/errors";
import { useOrgServers } from "@/lib/org-data";

// InstanceUser is one of an instance's users (Wings' InstanceUser).
type InstanceUser = {
  name: string;
  kind: "admin" | "database" | "readonly" | "other";
  server_id?: string;
  database_id?: string;
  reaches: string[]; // "*" is every database
  hosts?: string[];
};

type Tab = "databases" | "users" | "connections";

const instanceDatabasesKey = (nodeId: string, id: string) => ["db-instance-databases", nodeId, id];
const instanceUsersKey = (nodeId: string, id: string) => ["db-instance-users", nodeId, id];

// InstanceBrowser is an instance's own page (docs/DATABASES.md#the-browser),
// for admins: its databases of every server, each opened in the server's
// own browser (as that database's user); its users and what each can
// reach, with read-only users for outside tools; and every connection to
// it, as its administrator.
export function InstanceBrowser({
  orgId,
  nodeId,
  instanceId,
  userId,
  transfers,
  stream,
  usage: usageSupported,
}: {
  orgId: string;
  nodeId: string;
  instanceId: string;
  userId: string;
  // The node's Wings imports and exports, and has read-only users.
  transfers: boolean;
  // The node's Wings streams query results.
  stream: boolean;
  // The node's Wings shows instances' usage.
  usage: boolean;
}) {
  const insts = useInstances(nodeId);
  const inst = insts.data?.instances.find((i) => i.id === instanceId);
  const usage = useInstanceUsage(nodeId, usageSupported && inst?.state === "running");
  const servers = useOrgServers(orgId, 30_000).servers;
  const serverName = (id?: string) => servers.find((s) => s.id === id)?.name ?? "a server";
  const running = inst?.state === "running";
  const sql = !!inst && isSQL(inst.engine);
  const dbs = useQuery({
    queryKey: instanceDatabasesKey(nodeId, instanceId),
    queryFn: async () =>
      (
        await dbCommand<{ databases: Database[] }>(nodeId, "", "node.db.databases", {
          id: instanceId,
        })
      ).databases,
    enabled: sql,
    retry: false,
  });
  const [tab, setTab] = useState<Tab>("databases");
  const [picked, setPicked] = useState<string>();
  const list = dbs.data ?? [];
  const db = list.find((d) => d.id === picked) ?? list[0];

  const tabs: { id: Tab; label: string }[] = [
    { id: "databases", label: "Databases" },
    { id: "users", label: "Users" },
    { id: "connections", label: "Connections" },
  ];

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <Link
          to="/orgs/$orgId/nodes/$nodeId/databases"
          params={{ orgId, nodeId }}
          className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="size-4" /> Instances
        </Link>
        {inst && (
          <>
            <h2 className="text-base font-medium">{inst.name}</h2>
            <Badge variant="secondary">
              {engineName(inst.engine)} {inst.version}
            </Badge>
            <span className="font-mono text-xs text-muted-foreground">
              {inst.host}:{inst.port}
            </span>
          </>
        )}
      </div>

      {inst && running && usageSupported && (
        <InstanceUsageCards instance={inst} usage={usage.data?.[inst.id]} />
      )}

      {insts.error && <p className="text-sm text-destructive">{message(insts.error)}</p>}
      {insts.data && !inst && (
        <p className="text-sm text-muted-foreground">This instance isn't on the node any more.</p>
      )}
      {inst && !sql && (
        <p className="text-sm text-muted-foreground">
          {engineName(inst.engine)} databases are browsed from each server's Databases tab.
        </p>
      )}
      {inst && sql && !running && (
        <p className="text-sm text-muted-foreground">
          {inst.name} isn't running, so there's nothing to browse. Start it from the node's
          Databases tab.
        </p>
      )}

      {inst && sql && running && (
        <>
          <div role="tablist" aria-label="Instance" className="flex flex-wrap gap-1 border-b">
            {tabs.map((t) => (
              <button
                key={t.id}
                type="button"
                role="tab"
                aria-selected={tab === t.id}
                className={cn(
                  "-mb-px border-b-2 px-3 py-2 font-mono text-xs tracking-wide uppercase transition-colors",
                  tab === t.id
                    ? "border-primary text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
                onClick={() => setTab(t.id)}
              >
                {t.label}
              </button>
            ))}
          </div>

          {tab === "databases" && (
            <div className="grid gap-4 lg:grid-cols-[14rem_minmax(0,1fr)]">
              <nav aria-label="Databases" className="flex min-w-0 flex-col gap-0.5">
                {dbs.error && <p className="text-sm text-destructive">{message(dbs.error)}</p>}
                {dbs.isPending && <p className="text-sm text-muted-foreground">Asking the node…</p>}
                {dbs.data && list.length === 0 && (
                  <p className="text-sm text-muted-foreground">
                    No databases yet. Servers get one from their Databases tab.
                  </p>
                )}
                {list.map((d) => {
                  const on = db?.id === d.id;
                  return (
                    <button
                      key={d.id}
                      type="button"
                      aria-current={on ? "true" : undefined}
                      className={cn(
                        "flex items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted",
                        on && "bg-brand-wash hover:bg-brand-wash",
                      )}
                      onClick={() => setPicked(d.id)}
                    >
                      <DatabaseIcon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
                      <span className="flex min-w-0 flex-col">
                        <span className="truncate font-mono">{d.label}</span>
                        <span className="truncate text-xs text-muted-foreground">
                          {serverName(d.server_id)}
                        </span>
                      </span>
                    </button>
                  );
                })}
              </nav>
              <section className="min-w-0">
                {db && (
                  <DatabaseBrowser
                    key={db.id}
                    orgId={orgId}
                    nodeId={nodeId}
                    serverId={db.server_id}
                    id={db.id}
                    canWrite
                    transfers={transfers}
                    stream={stream}
                    embedded
                  />
                )}
              </section>
            </div>
          )}
          {tab === "users" && (
            <UsersView
              nodeId={nodeId}
              instanceId={instanceId}
              userId={userId}
              hosts={inst.engine !== "postgres"}
              databases={list}
              transfers={transfers}
              serverName={serverName}
            />
          )}
          {tab === "connections" && (
            <ProcessesTab nodeId={nodeId} serverId="" id="" canWrite instance={instanceId} />
          )}
        </>
      )}
    </div>
  );
}

// UsersView is an instance's users and what each can reach, and each
// database's read-only user for an outside tool.
function UsersView({
  nodeId,
  instanceId,
  userId,
  hosts,
  databases,
  transfers,
  serverName,
}: {
  nodeId: string;
  instanceId: string;
  userId: string;
  // MariaDB and MySQL: where each user may log in from.
  hosts: boolean;
  databases: Database[];
  transfers: boolean;
  serverName: (id?: string) => string;
}) {
  const client = useQueryClient();
  const users = useQuery({
    queryKey: instanceUsersKey(nodeId, instanceId),
    queryFn: async () =>
      (await dbCommand<{ users: InstanceUser[] }>(nodeId, "", "node.db.users", { id: instanceId }))
        .users,
    retry: false,
  });
  const [shown, setShown] = useState<{ db: Database; password: string }>();
  const label = (id?: string) => databases.find((d) => d.id === id)?.label ?? "a database";

  function what(u: InstanceUser): string {
    switch (u.kind) {
      case "admin":
        return "The instance's administrator, which only Raptor uses";
      case "database":
        return `${label(u.database_id)}'s own user, on ${serverName(u.server_id)}`;
      case "readonly":
        return `${label(u.database_id)}'s read-only user, on ${serverName(u.server_id)}`;
    }
    return "Made with SQL, or the engine's own";
  }

  function changed() {
    void client.invalidateQueries({ queryKey: instanceUsersKey(nodeId, instanceId) });
    void client.invalidateQueries({ queryKey: instanceDatabasesKey(nodeId, instanceId) });
  }

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>Users</CardTitle>
          <CardDescription>
            Everyone who can log in to this instance, and the databases each can use.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {users.error && <p className="text-sm text-destructive">{message(users.error)}</p>}
          {users.isPending && <p className="text-sm text-muted-foreground">Asking the node…</p>}
          {users.data && (
            <div className="overflow-x-auto rounded-lg border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>User</TableHead>
                    <TableHead>What it is</TableHead>
                    <TableHead>Can use</TableHead>
                    {hosts && <TableHead>Logs in from</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.data.map((u) => (
                    <TableRow key={u.name}>
                      <TableCell className="font-mono text-xs">{u.name}</TableCell>
                      <TableCell className="text-xs">{what(u)}</TableCell>
                      <TableCell className="font-mono text-xs">
                        {u.reaches.includes("*") ? (
                          <span className="font-sans">Every database</span>
                        ) : u.reaches.length ? (
                          u.reaches.join(", ")
                        ) : (
                          <span className="font-sans text-muted-foreground">Nothing</span>
                        )}
                      </TableCell>
                      {hosts && (
                        <TableCell className="font-mono text-xs">
                          {(u.hosts ?? []).join(", ")}
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {transfers && databases.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>Read-only users</CardTitle>
            <CardDescription>
              For a tool outside Raptor that should only look at one database: a stats page, a map,
              a backup script. Each reads every table of its database and changes nothing.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {databases.map((d) => (
              <div key={d.id} className="flex flex-col gap-1">
                <p className="text-sm">
                  <span className="font-mono">{d.label}</span>{" "}
                  <span className="text-muted-foreground">on {serverName(d.server_id)}</span>
                </p>
                <ReadOnlyUser
                  nodeId={nodeId}
                  serverId={d.server_id}
                  userId={userId}
                  db={d}
                  canWrite
                  onShow={(password, user) =>
                    setShown({ db: user ? { ...d, readonly_user: user } : d, password })
                  }
                  onChanged={changed}
                />
              </div>
            ))}
          </CardContent>
        </Card>
      )}
      {shown && <PasswordDialog {...shown} readonly onClose={() => setShown(undefined)} />}
    </div>
  );
}
