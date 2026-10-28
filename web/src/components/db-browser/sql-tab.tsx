import { useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Download, FileDown, Loader2, Play, Square } from "lucide-react";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";

import { Grid } from "@/components/db-browser/grid";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { csvText } from "@/lib/csv";
import {
  dbCommand,
  maxStreamTimeout,
  type Result,
  type StreamDone,
  streamQuery,
  type Table,
  tableKey,
} from "@/lib/db-browser";
import { message } from "@/lib/errors";
import { useFileSettings } from "@/lib/file-settings";

const MonacoEditor = lazy(() => import("@/components/monaco"));

// Rows on one page of a result.
const pageSize = 200;

// What a run shows: its last result's columns and rows, and how it ended
// (once it has).
type View = {
  columns: Result["columns"];
  rows: Result["rows"];
  done?: StreamDone;
};

// SQLTab is the SQL editor: completion from the schema, Ctrl/Cmd-Enter to
// run, the result in a grid a page at a time. With stream, results arrive
// as they're read, up to 10,000 rows, and Cancel stops the query on the
// engine; the whole result exports through onExport. Those who may only
// look run one statement at a time, read-only, as the database's reader.
export function SQLTab({
  nodeId,
  serverId,
  id,
  tables,
  canWrite,
  start,
  stream,
  onExport,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  tables: Table[];
  canWrite: boolean;
  // What the editor starts with (a table picked in the sidebar).
  start: string;
  // The node's Wings streams results (dbQueryStreamCapability).
  stream: boolean;
  // Exports a query's whole result into the server's files.
  onExport?: (sql: string) => void;
}) {
  const client = useQueryClient();
  const settings = useFileSettings();
  const text = useRef(start);
  const ran = useRef("");
  const [timeout, setTimeoutSecs] = useState("30");
  const [busy, setBusy] = useState(false);
  const [view, setView] = useState<View>();
  const [page, setPage] = useState(0);
  const [error, setError] = useState("");
  const [elapsed, setElapsed] = useState(0);
  const [cancelling, setCancelling] = useState(false);
  const abort = useRef<AbortController | null>(null);
  const queryId = useRef("");
  const completions = useMemo(
    () => [
      ...tables.map((t) => ({ label: tableKey(t), detail: "table" })),
      ...[...new Set(tables.flatMap((t) => t.columns.map((c) => c.name)))].map((c) => ({
        label: c,
        detail: "column",
      })),
    ],
    [tables],
  );
  const maxTimeout = stream ? maxStreamTimeout : 50;

  // Leaving the tab closes the stream, which stops the query.
  useEffect(() => () => abort.current?.abort(), []);

  useEffect(() => {
    if (!busy) return;
    const began = Date.now();
    const t = setInterval(() => setElapsed(Math.round((Date.now() - began) / 1000)), 1000);
    return () => clearInterval(t);
  }, [busy]);

  async function afterChange() {
    // A query may have changed the schema or the rows on show.
    if (canWrite) {
      await client.invalidateQueries({ queryKey: ["db-schema", nodeId, serverId, id] });
      await client.invalidateQueries({ queryKey: ["db-rows", nodeId, serverId, id] });
    }
  }

  async function runWhole(sql: string) {
    const res = await dbCommand<Result>(nodeId, serverId, canWrite ? "db.query" : "db.query.read", {
      id,
      sql,
      timeout: Number(timeout) || undefined,
    });
    setView({
      columns: res.affected >= 0 ? [] : res.columns,
      rows: res.rows,
      done: {
        statements: res.statements ?? 1,
        affected: res.affected,
        rows: res.rows.length,
        shown: res.rows.length,
        more: res.more,
        cut: res.cut,
        millis: res.millis,
      },
    });
  }

  async function runStreamed(sql: string) {
    const ctl = new AbortController();
    abort.current = ctl;
    queryId.current = "";
    let columns: Result["columns"] = [];
    let rows: Result["rows"] = [];
    let shown = 0;
    let timer: ReturnType<typeof setTimeout> | undefined;
    // Rows are put on screen a few times a second, not line by line.
    const show = () => {
      timer = undefined;
      shown = rows.length;
      setView({ columns, rows: rows.slice() });
    };
    try {
      await streamQuery({
        nodeId,
        serverId,
        action: canWrite ? "db.query" : "db.query.read",
        params: { id, sql, timeout: Number(timeout) || undefined },
        signal: ctl.signal,
        onLine: (line) => {
          if ("query_id" in line) queryId.current = line.query_id;
          else if ("columns" in line) {
            columns = line.columns;
            rows = [];
            show();
          } else if ("rows" in line) {
            rows.push(...line.rows);
            if (!timer && rows.length !== shown) timer = setTimeout(show, 150);
          } else if ("done" in line) {
            clearTimeout(timer);
            const done = line.done;
            setView(done.affected >= 0 ? { columns: [], rows: [], done } : { columns, rows, done });
          } else if ("error" in line) {
            clearTimeout(timer);
            setView(columns.length ? { columns, rows: rows.slice() } : undefined);
            setError(line.error);
          }
        },
      });
    } catch (err) {
      clearTimeout(timer);
      if (ctl.signal.aborted) {
        setView(columns.length ? { columns, rows: rows.slice() } : undefined);
        setError("The query was cancelled.");
      } else throw err;
    } finally {
      abort.current = null;
      queryId.current = "";
    }
  }

  async function run() {
    if (busy || !text.current.trim()) return;
    const sql = text.current;
    ran.current = sql;
    setBusy(true);
    setError("");
    setView(undefined);
    setPage(0);
    setElapsed(0);
    try {
      if (stream) await runStreamed(sql);
      else await runWhole(sql);
      await afterChange();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
      setCancelling(false);
    }
  }

  // cancel asks the node to stop the query on the engine; the stream then
  // ends with why. Closing the stream does it too, if the node can't be
  // asked.
  async function cancel() {
    setCancelling(true);
    const ctl = abort.current;
    try {
      if (queryId.current) {
        await dbCommand(nodeId, serverId, "db.query.cancel", { query_id: queryId.current });
        setTimeout(() => ctl?.abort(), 5_000);
        return;
      }
    } catch {
      // closing the stream stops it as well
    }
    ctl?.abort();
  }

  const done = view?.done;
  const rows = view?.rows ?? [];
  const pages = Math.max(1, Math.ceil(rows.length / pageSize));
  const onPage = Math.min(page, pages - 1);
  const pageView: Result | undefined = view && {
    columns: view.columns,
    rows: rows.slice(onPage * pageSize, (onPage + 1) * pageSize),
    affected: -1,
    millis: 0,
  };

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <Card className="h-56 overflow-hidden py-0">
        <Suspense
          fallback={<p className="p-4 text-sm text-muted-foreground">Loading the editor…</p>}
        >
          <MonacoEditor
            value={start}
            language="sql"
            readOnly={false}
            settings={settings}
            completions={completions}
            onChange={(t) => {
              text.current = t;
            }}
            onSave={() => {}}
            onRun={run}
          />
        </Suspense>
      </Card>
      <div className="flex flex-wrap items-center gap-3">
        {busy && stream ? (
          <Button size="sm" variant="outline" disabled={cancelling} onClick={cancel}>
            {cancelling ? <Loader2 className="animate-spin" /> : <Square />} Cancel
          </Button>
        ) : (
          <Button size="sm" disabled={busy} onClick={run}>
            {busy ? <Loader2 className="animate-spin" /> : <Play />} Run
          </Button>
        )}
        <span className="text-xs text-muted-foreground">
          {busy
            ? `Running · ${elapsed} s${rows.length ? ` · ${rows.length.toLocaleString()} rows so far` : ""}`
            : "Ctrl or ⌘ + Enter"}
        </span>
        <label
          htmlFor="sql-timeout"
          className="ml-auto flex items-center gap-2 text-xs text-muted-foreground"
        >
          Stop after
          <Input
            id="sql-timeout"
            type="number"
            min={1}
            max={maxTimeout}
            className="h-7 w-16"
            value={timeout}
            onChange={(e) => setTimeoutSecs(e.target.value)}
          />
          seconds
        </label>
      </div>
      <p className="text-xs text-muted-foreground">
        {canWrite
          ? "Any SQL runs, as the database's own user, which can't reach other databases. With several statements, the last one's result shows."
          : "You can look, not change: one statement at a time, as a user that can only read."}
      </p>
      {error && <p className="font-mono text-sm whitespace-pre-wrap text-destructive">{error}</p>}
      {view && (done || view.columns.length > 0) && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-xs text-muted-foreground">
              {summary(view, busy)}
              {done && done.statements > 1 ? ` · ${done.statements} statements` : ""}
              {done ? ` · ${done.millis.toLocaleString()} ms` : ""}
              {done?.cut ? " · long values are cut" : ""}
            </p>
            {view.columns.length > 0 && rows.length > 0 && !busy && (
              <div className="flex gap-1">
                {done?.more && onExport && (
                  <Button size="sm" variant="ghost" onClick={() => onExport(ran.current)}>
                    <FileDown /> Export the whole result
                  </Button>
                )}
                <Button size="sm" variant="ghost" onClick={() => downloadCSV(view)}>
                  <Download /> CSV{done?.more ? " of these" : ""}
                </Button>
              </div>
            )}
          </div>
          {pageView && view.columns.length > 0 && <Grid result={pageView} />}
          {pages > 1 && (
            <div className="flex items-center justify-end gap-2 text-xs text-muted-foreground">
              <span className="tabular-nums">
                {(onPage * pageSize + 1).toLocaleString()}–
                {Math.min((onPage + 1) * pageSize, rows.length).toLocaleString()} of{" "}
                {rows.length.toLocaleString()}
              </span>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="Previous page"
                disabled={onPage === 0}
                onClick={() => setPage(onPage - 1)}
              >
                <ChevronLeft />
              </Button>
              <Button
                size="icon-sm"
                variant="ghost"
                aria-label="Next page"
                disabled={onPage >= pages - 1}
                onClick={() => setPage(onPage + 1)}
              >
                <ChevronRight />
              </Button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// summary says what a run did, or is doing.
function summary(view: View, running: boolean): string {
  const done = view.done;
  const n = (k: number) => `${k.toLocaleString()} ${k === 1 ? "row" : "rows"}`;
  if (!done) return running ? `${n(view.rows.length)} so far` : n(view.rows.length);
  if (done.affected >= 0) return `Done: ${n(done.affected)} changed`;
  if (!done.more) return n(done.shown);
  return done.rows > done.shown
    ? `The first ${done.shown.toLocaleString()} of ${n(done.rows)} are shown`
    : `The first ${n(done.shown)} are shown`;
}

// downloadCSV saves the rows on screen as a CSV file. Export writes every
// row of the result into the server's files instead.
function downloadCSV(view: View) {
  const url = URL.createObjectURL(
    new Blob([csvText({ columns: view.columns, rows: view.rows })], {
      type: "text/csv",
    }),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = "query.csv";
  a.click();
  URL.revokeObjectURL(url);
}
