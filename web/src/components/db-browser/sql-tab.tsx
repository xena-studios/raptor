import { useQueryClient } from "@tanstack/react-query";
import { Download, Loader2, Play } from "lucide-react";
import { lazy, Suspense, useMemo, useRef, useState } from "react";

import { Grid } from "@/components/db-browser/grid";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { csvText } from "@/lib/csv";
import { dbCommand, type Result, type Table, tableKey } from "@/lib/db-browser";
import { message } from "@/lib/errors";
import { useFileSettings } from "@/lib/file-settings";

const MonacoEditor = lazy(() => import("@/components/monaco"));

// SQLTab is the SQL editor: completion from the schema, Ctrl/Cmd-Enter to
// run, the result in a grid. Those who may only look run one statement at
// a time, read-only.
export function SQLTab({
  nodeId,
  serverId,
  id,
  tables,
  canWrite,
  start,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  tables: Table[];
  canWrite: boolean;
  // What the editor starts with (a table picked in the sidebar).
  start: string;
}) {
  const client = useQueryClient();
  const settings = useFileSettings();
  const text = useRef(start);
  const [timeout, setTimeoutSecs] = useState("30");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Result>();
  const [error, setError] = useState("");
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

  async function run() {
    if (busy || !text.current.trim()) return;
    setBusy(true);
    setError("");
    try {
      const res = await dbCommand<Result>(
        nodeId,
        serverId,
        canWrite ? "db.query" : "db.query.read",
        {
          id,
          sql: text.current,
          timeout: Number(timeout) || undefined,
        },
      );
      setResult(res);
      // A query may have changed the schema or the rows on show.
      if (canWrite) {
        await client.invalidateQueries({ queryKey: ["db-schema", nodeId, serverId, id] });
        await client.invalidateQueries({ queryKey: ["db-rows", nodeId, serverId, id] });
      }
    } catch (err) {
      setResult(undefined);
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

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
        <Button size="sm" disabled={busy} onClick={run}>
          {busy ? <Loader2 className="animate-spin" /> : <Play />} Run
        </Button>
        <span className="text-xs text-muted-foreground">Ctrl or ⌘ + Enter</span>
        <label
          htmlFor="sql-timeout"
          className="ml-auto flex items-center gap-2 text-xs text-muted-foreground"
        >
          Stop after
          <Input
            id="sql-timeout"
            type="number"
            min={1}
            max={50}
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
          : "You can look, not change: one statement at a time, in a read-only transaction."}
      </p>
      {error && <p className="font-mono text-sm whitespace-pre-wrap text-destructive">{error}</p>}
      {result && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-xs text-muted-foreground">
              {result.affected >= 0
                ? `Done: ${result.affected} ${result.affected === 1 ? "row" : "rows"} changed`
                : `${result.rows.length}${result.more ? "+" : ""} ${result.rows.length === 1 ? "row" : "rows"}`}
              {result.statements && result.statements > 1
                ? ` · ${result.statements} statements`
                : ""}
              {` · ${result.millis} ms`}
              {result.more ? " · only the first 1,000 rows are shown" : ""}
              {result.cut ? " · long values are cut" : ""}
            </p>
            {result.columns.length > 0 && result.rows.length > 0 && (
              <Button size="sm" variant="ghost" onClick={() => downloadCSV(result)}>
                <Download /> CSV
              </Button>
            )}
          </div>
          {result.columns.length > 0 && <Grid result={result} />}
        </div>
      )}
    </div>
  );
}

// downloadCSV saves the rows on screen as a CSV file. For a whole table,
// Export writes one on the node instead, with every row.
function downloadCSV(result: Result) {
  const url = URL.createObjectURL(new Blob([csvText(result)], { type: "text/csv" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = "query.csv";
  a.click();
  URL.revokeObjectURL(url);
}
