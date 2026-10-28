import { Link } from "@tanstack/react-router";
import { cn } from "cn";
import { ArrowLeft, Download, Eye, Plus, Search, Table2, Upload } from "lucide-react";
import { useState } from "react";

import { ProcessesTab } from "@/components/db-browser/processes";
import { RowsTab } from "@/components/db-browser/rows";
import { SQLTab } from "@/components/db-browser/sql-tab";
import {
  type Change,
  ChangeDialog,
  NewTableDialog,
  StructureTab,
} from "@/components/db-browser/structure";
import { TransferDialog } from "@/components/db-browser/transfer";
import { DocumentBrowser } from "@/components/db-docs/documents";
import { KeyBrowser } from "@/components/db-docs/keys";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { engineName, useServerDatabases } from "@/lib/databases";
import { formatBytes, tableKey, tableRef, useSchema } from "@/lib/db-browser";
import { message } from "@/lib/errors";

type Tab = "rows" | "structure" | "sql" | "processes";

// DatabaseBrowser is one of a server's databases, opened: its tables on
// the left, and on the right a table's rows or structure, the SQL editor,
// or the connections to it (docs/DATABASES.md#the-browser).
export function DatabaseBrowser({
  orgId,
  nodeId,
  serverId,
  id,
  canWrite,
  transfers,
  stream,
  embedded,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  id: string;
  canWrite: boolean;
  // The node's Wings can import and export.
  transfers: boolean;
  // The node's Wings streams query results (dbQueryStreamCapability).
  stream: boolean;
  // Shown inside an instance's page, which has its own way back.
  embedded?: boolean;
}) {
  const list = useServerDatabases(nodeId, serverId);
  const db = list.data?.databases.find((d) => d.id === id);
  // Redis and MongoDB have browsers of their own (components/db-docs).
  const nosql = db?.engine === "redis" || db?.engine === "mongodb";
  const schema = useSchema(nodeId, serverId, id, !!db && !nosql);
  const [collection, setCollection] = useState<string>();
  const [picked, setPicked] = useState<string>();
  const [tab, setTab] = useState<Tab>("rows");
  const [query, setQuery] = useState("");
  const [change, setChange] = useState<Change>();
  const [creating, setCreating] = useState(false);
  const [sqlStart, setSqlStart] = useState("");
  const [transfer, setTransfer] = useState<"import" | "export">();
  const [exportQuery, setExportQuery] = useState<string>();

  const tables = schema.data ?? [];
  const table = tables.find((t) => tableKey(t) === picked) ?? tables[0];
  const shown = tables.filter((t) => tableKey(t).toLowerCase().includes(query.toLowerCase()));
  const engine = db?.engine;

  const tabs: { id: Tab; label: string }[] = [
    { id: "rows", label: "Rows" },
    { id: "structure", label: "Structure" },
    { id: "sql", label: "SQL" },
    { id: "processes", label: "Processes" },
  ];

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        {!embedded && (
          <Link
            to="/orgs/$orgId/servers/$serverId/databases"
            params={{ orgId, serverId }}
            className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="size-4" /> Databases
          </Link>
        )}
        {db && (
          <>
            <h2 className="font-mono text-base">{db.label}</h2>
            <Badge variant="secondary">{engineName(db.engine)}</Badge>
            <span className="font-mono text-xs text-muted-foreground">{db.name}</span>
          </>
        )}
        {!canWrite && (
          <span className="ml-auto inline-flex items-center gap-1.5 text-xs text-muted-foreground">
            <Eye className="size-3.5" /> Read-only for you
          </span>
        )}
        {canWrite && transfers && db?.engine !== "redis" && (
          <div className="ml-auto flex gap-2">
            <Button size="sm" variant="outline" onClick={() => setTransfer("import")}>
              <Upload /> Import
            </Button>
            <Button size="sm" variant="outline" onClick={() => setTransfer("export")}>
              <Download /> Export
            </Button>
          </div>
        )}
      </div>

      {db?.engine === "redis" && (
        <KeyBrowser nodeId={nodeId} serverId={serverId} id={id} canWrite={canWrite} />
      )}
      {db?.engine === "mongodb" && (
        <DocumentBrowser
          nodeId={nodeId}
          serverId={serverId}
          id={id}
          canWrite={canWrite}
          onCollection={setCollection}
        />
      )}
      {(list.error || schema.error) && (
        <p className="text-sm text-destructive">{message(list.error ?? schema.error)}</p>
      )}

      {db && !nosql && (
        <div className="grid gap-4 lg:grid-cols-[14rem_minmax(0,1fr)]">
          <aside className="flex min-w-0 flex-col gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
              <Input
                aria-label="Find a table"
                placeholder="Tables"
                className="pl-8"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
            {schema.isPending && <p className="text-sm text-muted-foreground">Reading tables…</p>}
            {schema.data && tables.length === 0 && (
              <p className="text-sm text-muted-foreground">
                No tables yet. Plugins make theirs when they first connect.
              </p>
            )}
            <nav aria-label="Tables" className="flex max-h-[60vh] flex-col gap-0.5 overflow-y-auto">
              {shown.map((t) => {
                const k = tableKey(t);
                const on = table && tableKey(table) === k;
                return (
                  <button
                    key={k}
                    type="button"
                    aria-current={on ? "true" : undefined}
                    className={cn(
                      "flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted",
                      on && "bg-brand-wash hover:bg-brand-wash",
                    )}
                    onClick={() => {
                      setPicked(k);
                      if (tab === "processes" || tab === "sql") setTab("rows");
                    }}
                  >
                    {t.kind === "view" ? (
                      <Eye className="size-3.5 shrink-0 text-muted-foreground" />
                    ) : (
                      <Table2 className="size-3.5 shrink-0 text-muted-foreground" />
                    )}
                    <span className="min-w-0 flex-1 truncate font-mono">{k}</span>
                    <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                      {t.rows.toLocaleString()}
                    </span>
                  </button>
                );
              })}
            </nav>
            {canWrite && engine && (
              <Button
                size="sm"
                variant="outline"
                className="self-start"
                onClick={() => setCreating(true)}
              >
                <Plus /> New table
              </Button>
            )}
          </aside>

          <section className="flex min-w-0 flex-col gap-4">
            <div
              role="tablist"
              aria-label="Database"
              className="flex flex-wrap items-center gap-1 border-b"
            >
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
                  onClick={() => {
                    if (t.id === "sql" && table && engine) {
                      setSqlStart(`SELECT * FROM ${tableRef(engine, table)} LIMIT 100;`);
                    }
                    setTab(t.id);
                  }}
                >
                  {t.label}
                </button>
              ))}
              {table && (tab === "rows" || tab === "structure") && (
                <span className="ml-auto pb-1 text-xs text-muted-foreground">
                  {tableKey(table)} · about {table.rows.toLocaleString()} rows ·{" "}
                  {formatBytes(table.bytes)}
                </span>
              )}
            </div>

            {tab === "rows" &&
              (table ? (
                <RowsTab
                  key={tableKey(table)}
                  nodeId={nodeId}
                  serverId={serverId}
                  id={id}
                  table={table}
                  canWrite={canWrite}
                />
              ) : (
                schema.data && <p className="text-sm text-muted-foreground">Pick a table.</p>
              ))}
            {tab === "structure" && table && engine && (
              <StructureTab
                engine={engine}
                table={table}
                canWrite={canWrite}
                onChange={setChange}
              />
            )}
            {tab === "sql" && (
              <SQLTab
                nodeId={nodeId}
                serverId={serverId}
                id={id}
                tables={tables}
                canWrite={canWrite}
                start={sqlStart}
                stream={stream}
                onExport={canWrite && transfers && stream ? setExportQuery : undefined}
              />
            )}
            {tab === "processes" && (
              <ProcessesTab nodeId={nodeId} serverId={serverId} id={id} canWrite={canWrite} />
            )}
          </section>
        </div>
      )}

      {creating && engine && (
        <NewTableDialog
          engine={engine}
          onSubmit={(c) => {
            setCreating(false);
            setChange(c);
          }}
          onClose={() => setCreating(false)}
        />
      )}
      {transfer && (
        <TransferDialog
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          id={id}
          kind={transfer}
          table={table}
          mongo={db?.engine === "mongodb"}
          collection={collection}
          onClose={() => setTransfer(undefined)}
        />
      )}
      {exportQuery !== undefined && (
        <TransferDialog
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          id={id}
          kind="export"
          query={exportQuery}
          onClose={() => setExportQuery(undefined)}
        />
      )}
      {change && (
        <ChangeDialog
          nodeId={nodeId}
          serverId={serverId}
          id={id}
          change={change}
          onDone={() => setChange(undefined)}
          onClose={() => setChange(undefined)}
        />
      )}
    </div>
  );
}
