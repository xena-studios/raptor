import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, Loader2, Plus, RefreshCw, Trash2, X } from "lucide-react";
import { type FormEvent, useState } from "react";

import { useConfirm } from "@/components/confirm";
import { Grid } from "@/components/db-browser/grid";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { dbCommand, type Filter, type Result, type Table, tableKey } from "@/lib/db-browser";
import { message } from "@/lib/errors";

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

const ops: { op: Filter["op"]; label: string; value: boolean }[] = [
  { op: "contains", label: "contains", value: true },
  { op: "=", label: "is", value: true },
  { op: "!=", label: "isn't", value: true },
  { op: ">", label: ">", value: true },
  { op: ">=", label: "≥", value: true },
  { op: "<", label: "<", value: true },
  { op: "<=", label: "≤", value: true },
  { op: "null", label: "is NULL", value: false },
  { op: "notnull", label: "isn't NULL", value: false },
];

// RowsTab pages through a table's rows: filtered, sorted, and, for those
// who may change the database, edited, inserted, and deleted by primary
// key.
export function RowsTab({
  nodeId,
  serverId,
  id,
  table,
  canWrite,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  table: Table;
  canWrite: boolean;
}) {
  const client = useQueryClient();
  const ask = useConfirm();
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(100);
  const [sort, setSort] = useState("");
  const [desc, setDesc] = useState(false);
  const [filters, setFilters] = useState<Filter[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<(string | null)[] | "new">();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const params = {
    id,
    schema: table.schema,
    table: table.name,
    offset,
    limit,
    sort,
    desc,
    filters,
  };
  const key = ["db-rows", nodeId, serverId, id, tableKey(table), params];
  const rows = useQuery({
    queryKey: key,
    queryFn: () => dbCommand<Result>(nodeId, serverId, "db.rows", params),
    placeholderData: keepPreviousData,
    retry: false,
  });

  const pk = table.primary_key;
  const editable = canWrite && table.kind === "table" && pk.length > 0;
  const colIndex = (name: string) => table.columns.findIndex((c) => c.name === name);
  const keyOf = (row: (string | null)[]) => JSON.stringify(pk.map((c) => row[colIndex(c)]));
  const refresh = () => client.invalidateQueries({ queryKey: ["db-rows", nodeId, serverId, id] });

  async function removeSelected() {
    const n = selected.size;
    const ok = await ask.confirm({
      title: `Delete ${n} ${n === 1 ? "row" : "rows"}?`,
      description: `They're deleted from ${table.name} for good.`,
      confirm: "Delete",
      destructive: true,
    });
    if (!ok) return;
    setBusy(true);
    setError("");
    try {
      const keys = [...selected].map((k) => {
        const vals = JSON.parse(k) as (string | null)[];
        return Object.fromEntries(pk.map((c, i) => [c, vals[i]]));
      });
      await dbCommand(nodeId, serverId, "db.row.delete", {
        id,
        schema: table.schema,
        table: table.name,
        keys,
      });
      setSelected(new Set());
      await refresh();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const data = rows.data;
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <FilterBar
          table={table}
          filters={filters}
          onChange={(f) => {
            setFilters(f);
            setOffset(0);
          }}
        />
        <div className="ml-auto flex gap-2">
          {editable && selected.size > 0 && (
            <Button size="sm" variant="destructive" disabled={busy} onClick={removeSelected}>
              {busy ? <Loader2 className="animate-spin" /> : <Trash2 />} Delete {selected.size}
            </Button>
          )}
          {canWrite && table.kind === "table" && (
            <Button size="sm" variant="outline" onClick={() => setOpen("new")}>
              <Plus /> Insert row
            </Button>
          )}
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Refresh"
            disabled={rows.isFetching}
            onClick={refresh}
          >
            <RefreshCw className={rows.isFetching ? "animate-spin" : undefined} />
          </Button>
        </div>
      </div>

      {canWrite && table.kind === "table" && pk.length === 0 && (
        <p className="text-xs text-muted-foreground">
          This table has no primary key, so its rows can't be changed here (there'd be no safe way
          to say which one). Use the SQL tab.
        </p>
      )}
      {(rows.error || error) && (
        <p className="text-sm text-destructive">{error || message(rows.error)}</p>
      )}
      {data && (
        <>
          {data.rows.length === 0 ? (
            <p className="rounded-lg border p-6 text-center text-sm text-muted-foreground">
              {filters.length ? "No rows match." : "The table is empty."}
            </p>
          ) : (
            <Grid
              result={data}
              sort={sort}
              desc={desc}
              onSort={(c) => {
                if (sort === c) setDesc(!desc);
                else {
                  setSort(c);
                  setDesc(false);
                }
                setOffset(0);
              }}
              selected={editable ? selected : undefined}
              onSelect={(k, on) => {
                const next = new Set(selected);
                if (on) next.add(k);
                else next.delete(k);
                setSelected(next);
              }}
              keyOf={pk.length ? keyOf : undefined}
              onOpen={(row) => setOpen(row)}
            />
          )}
          <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
            <span>
              {data.rows.length
                ? `Rows ${offset + 1}–${offset + data.rows.length}`
                : `Nothing from row ${offset + 1}`}
              {table.rows > 0 && !filters.length ? ` of about ${table.rows.toLocaleString()}` : ""}{" "}
              · {data.millis} ms
              {data.cut ? " · long values are cut; open a row to see them" : ""}
            </span>
            <div className="ml-auto flex items-center gap-2">
              <select
                aria-label="Rows per page"
                className={select}
                value={limit}
                onChange={(e) => {
                  setLimit(Number(e.target.value));
                  setOffset(0);
                }}
              >
                {[50, 100, 500, 1000].map((n) => (
                  <option key={n} value={n}>
                    {n} a page
                  </option>
                ))}
              </select>
              <Button
                size="icon-sm"
                variant="outline"
                aria-label="Previous page"
                disabled={offset === 0}
                onClick={() => setOffset(Math.max(0, offset - limit))}
              >
                <ChevronLeft />
              </Button>
              <Button
                size="icon-sm"
                variant="outline"
                aria-label="Next page"
                disabled={!data.more}
                onClick={() => setOffset(offset + limit)}
              >
                <ChevronRight />
              </Button>
            </div>
          </div>
        </>
      )}
      {open && (
        <RowDialog
          nodeId={nodeId}
          serverId={serverId}
          id={id}
          table={table}
          row={open === "new" ? undefined : open}
          editable={open === "new" || editable}
          onDone={async () => {
            setOpen(undefined);
            await refresh();
          }}
          onClose={() => setOpen(undefined)}
        />
      )}
    </div>
  );
}

function FilterBar({
  table,
  filters,
  onChange,
}: {
  table: Table;
  filters: Filter[];
  onChange: (f: Filter[]) => void;
}) {
  const [column, setColumn] = useState(table.columns[0]?.name ?? "");
  const [op, setOp] = useState<Filter["op"]>("contains");
  const [value, setValue] = useState("");
  const needsValue = ops.find((o) => o.op === op)?.value ?? true;

  function add(e: FormEvent) {
    e.preventDefault();
    if (!column || (needsValue && value === "")) return;
    onChange([...filters, { column, op, value: needsValue ? value : undefined }]);
    setValue("");
  }

  return (
    <form onSubmit={add} className="flex flex-wrap items-center gap-2">
      {filters.map((f, i) => (
        <span
          // biome-ignore lint/suspicious/noArrayIndexKey: filters are positional
          key={i}
          className="inline-flex items-center gap-1 rounded-md border px-2 py-0.5 font-mono text-xs"
        >
          {f.column} {ops.find((o) => o.op === f.op)?.label} {f.value}
          <button
            type="button"
            aria-label="Remove filter"
            className="text-muted-foreground hover:text-foreground"
            onClick={() => onChange(filters.filter((_, j) => j !== i))}
          >
            <X className="size-3" />
          </button>
        </span>
      ))}
      <select
        aria-label="Column"
        className={select}
        value={column}
        onChange={(e) => setColumn(e.target.value)}
      >
        {table.columns.map((c) => (
          <option key={c.name} value={c.name}>
            {c.name}
          </option>
        ))}
      </select>
      <select
        aria-label="Condition"
        className={select}
        value={op}
        onChange={(e) => setOp(e.target.value as Filter["op"])}
      >
        {ops.map((o) => (
          <option key={o.op} value={o.op}>
            {o.label}
          </option>
        ))}
      </select>
      {needsValue && (
        <Input
          aria-label="Value"
          className="h-8 w-40"
          placeholder="Value"
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
      )}
      <Button type="submit" size="sm" variant="outline">
        Filter
      </Button>
    </form>
  );
}

type Field = { mode: "value" | "null" | "default"; text: string };

// RowDialog shows a whole row, and changes it or inserts a new one. Only
// fields that changed are sent.
function RowDialog({
  nodeId,
  serverId,
  id,
  table,
  row,
  editable,
  onDone,
  onClose,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  table: Table;
  row?: (string | null)[];
  editable: boolean;
  onDone: () => void;
  onClose: () => void;
}) {
  const start = table.columns.map<Field>((c, i) => {
    if (!row) return { mode: c.auto || c.default != null ? "default" : "value", text: "" };
    const v = row[i];
    return v === null || v === undefined ? { mode: "null", text: "" } : { mode: "value", text: v };
  });
  const [fields, setFields] = useState(start);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const set = (i: number, f: Partial<Field>) =>
    setFields(fields.map((x, j) => (j === i ? { ...x, ...f } : x)));

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const values: Record<string, string | null> = {};
    table.columns.forEach((c, i) => {
      const f = fields[i];
      const s = start[i];
      if (!f || f.mode === "default") return;
      if (row && s && f.mode === s.mode && f.text === s.text) return;
      values[c.name] = f.mode === "null" ? null : f.text;
    });
    try {
      if (row) {
        const key = Object.fromEntries(
          table.primary_key.map((c) => [c, row[table.columns.findIndex((x) => x.name === c)]]),
        );
        await dbCommand(nodeId, serverId, "db.row.update", {
          id,
          schema: table.schema,
          table: table.name,
          key,
          values,
        });
      } else {
        await dbCommand(nodeId, serverId, "db.row.insert", {
          id,
          schema: table.schema,
          table: table.name,
          values,
        });
      }
      onDone();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const changed = fields.some((f, i) => f.mode !== start[i]?.mode || f.text !== start[i]?.text);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <form onSubmit={save} className="flex min-h-0 flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{row ? (editable ? "Edit row" : "Row") : "Insert row"}</DialogTitle>
            <DialogDescription>
              {row
                ? `In ${table.name}.${editable ? " Only the fields you change are saved." : ""}`
                : `Into ${table.name}. Fields left on Default get the column's default.`}
            </DialogDescription>
          </DialogHeader>
          <div className="flex max-h-[60vh] flex-col gap-3 overflow-y-auto pr-1">
            {table.columns.map((c, i) => {
              const f = fields[i] ?? { mode: "value", text: "" };
              const long = f.text.length > 60 || f.text.includes("\n");
              // Values over 8 KB arrive cut; saving one would save the cut.
              const tooLong =
                !!row && new TextEncoder().encode(start[i]?.text ?? "").length >= 8000;
              const canEdit = editable && !tooLong;
              return (
                <div key={c.name} className="flex flex-col gap-1.5">
                  <div className="flex items-center justify-between gap-2">
                    <label
                      htmlFor={`field-${i}`}
                      className="font-mono text-xs tracking-wide text-muted-foreground"
                    >
                      {c.name} <span className="opacity-60">{c.type}</span>
                      {table.primary_key.includes(c.name) && " · key"}
                    </label>
                    {canEdit && (
                      <select
                        aria-label={`${c.name}: what to set`}
                        className={`${select} h-6 text-xs`}
                        value={f.mode}
                        onChange={(e) => set(i, { mode: e.target.value as Field["mode"] })}
                      >
                        <option value="value">Value</option>
                        {c.nullable && <option value="null">NULL</option>}
                        {!row && <option value="default">Default</option>}
                      </select>
                    )}
                  </div>
                  {f.mode === "value" &&
                    (long ? (
                      <textarea
                        id={`field-${i}`}
                        readOnly={!canEdit}
                        rows={Math.min(10, f.text.split("\n").length + 1)}
                        className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 font-mono text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
                        value={f.text}
                        onChange={(e) => set(i, { text: e.target.value })}
                      />
                    ) : (
                      <Input
                        id={`field-${i}`}
                        readOnly={!canEdit}
                        className="font-mono"
                        value={f.text}
                        onChange={(e) => set(i, { text: e.target.value })}
                      />
                    ))}
                  {tooLong && (
                    <p className="text-xs text-muted-foreground">
                      Shown cut: it's over 8 KB. Change it with SQL.
                    </p>
                  )}
                </div>
              );
            })}
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {editable ? "Cancel" : "Close"}
            </Button>
            {editable && (
              <Button type="submit" disabled={busy || (!!row && !changed)}>
                {busy && <Loader2 className="animate-spin" />} {row ? "Save" : "Insert"}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
