import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, Loader2, Plus, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";

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
import { Label } from "@/components/ui/label";
import {
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Table as UITable,
} from "@/components/ui/table";
import type { Engine } from "@/lib/databases";
import {
  addColumnSQL,
  addIndexSQL,
  createTableSQL,
  dbCommand,
  dropColumnSQL,
  dropIndexSQL,
  dropTableSQL,
  type NewColumn,
  renameTableSQL,
  type Table,
  typeSuggestions,
} from "@/lib/db-browser";
import { message } from "@/lib/errors";

// A planned change: the SQL it runs, shown before it runs.
export type Change = { title: string; sql: string; destructive?: boolean; confirmName?: string };

// StructureTab shows a table's columns and indexes, and, for those who may
// change the database, changes them through forms that show their SQL.
export function StructureTab({
  engine,
  table,
  canWrite,
  onChange,
}: {
  engine: Engine;
  table: Table;
  canWrite: boolean;
  onChange: (c: Change) => void;
}) {
  const [adding, setAdding] = useState<"column" | "index" | "rename">();
  return (
    <div className="flex min-w-0 flex-col gap-6">
      <section className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium">Columns</h3>
          {canWrite && table.kind === "table" && (
            <Button size="sm" variant="outline" onClick={() => setAdding("column")}>
              <Plus /> Add column
            </Button>
          )}
        </div>
        <div className="overflow-x-auto rounded-lg border">
          <UITable>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>NULL</TableHead>
                <TableHead>Default</TableHead>
                {canWrite && table.kind === "table" && <TableHead className="w-10" />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {table.columns.map((c) => (
                <TableRow key={c.name}>
                  <TableCell className="font-mono">
                    <span className="inline-flex items-center gap-1.5">
                      {table.primary_key.includes(c.name) && (
                        <KeyRound
                          className="size-3.5 text-muted-foreground"
                          aria-label="Primary key"
                        />
                      )}
                      {c.name}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {c.type}
                    {c.auto && <span className="ml-1.5 text-muted-foreground">auto</span>}
                  </TableCell>
                  <TableCell className="text-xs">{c.nullable ? "Yes" : "No"}</TableCell>
                  <TableCell className="max-w-[16rem] truncate font-mono text-xs text-muted-foreground">
                    {c.default ?? ""}
                  </TableCell>
                  {canWrite && table.kind === "table" && (
                    <TableCell>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={`Drop column ${c.name}`}
                        disabled={table.columns.length < 2}
                        onClick={() =>
                          onChange({
                            title: `Drop column ${c.name}?`,
                            sql: dropColumnSQL(engine, table, c.name),
                            destructive: true,
                          })
                        }
                      >
                        <Trash2 />
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </UITable>
        </div>
      </section>

      {table.kind === "table" && (
        <section className="flex flex-col gap-2">
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-sm font-medium">Indexes</h3>
            {canWrite && (
              <Button size="sm" variant="outline" onClick={() => setAdding("index")}>
                <Plus /> Add index
              </Button>
            )}
          </div>
          {table.indexes.length === 0 ? (
            <p className="text-sm text-muted-foreground">No indexes.</p>
          ) : (
            <ul className="flex flex-col divide-y rounded-lg border">
              {table.indexes.map((x) => (
                <li key={x.name} className="flex items-center justify-between gap-3 px-3 py-2">
                  <span className="flex min-w-0 flex-wrap items-center gap-2 text-sm">
                    <span className="font-mono">{x.name}</span>
                    <span className="font-mono text-xs text-muted-foreground">
                      ({x.columns.join(", ")})
                    </span>
                    {x.primary ? (
                      <span className="text-xs text-muted-foreground">primary key</span>
                    ) : (
                      x.unique && <span className="text-xs text-muted-foreground">unique</span>
                    )}
                  </span>
                  {canWrite && !x.primary && (
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Drop index ${x.name}`}
                      onClick={() =>
                        onChange({
                          title: `Drop index ${x.name}?`,
                          sql: dropIndexSQL(engine, table, x.name),
                          destructive: true,
                        })
                      }
                    >
                      <Trash2 />
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {canWrite && (
        <section className="flex flex-wrap gap-2 border-t pt-4">
          {table.kind === "table" && (
            <Button size="sm" variant="outline" onClick={() => setAdding("rename")}>
              Rename table
            </Button>
          )}
          <Button
            size="sm"
            variant="outline"
            className="text-destructive"
            onClick={() =>
              onChange({
                title: `Drop ${table.kind} ${table.name}?`,
                sql: dropTableSQL(engine, table),
                destructive: true,
                confirmName: table.name,
              })
            }
          >
            <Trash2 /> Drop {table.kind}
          </Button>
        </section>
      )}

      {adding === "column" && (
        <FormDialog title="Add a column" onClose={() => setAdding(undefined)}>
          {(close) => (
            <ColumnForm
              engine={engine}
              onSubmit={(c) => {
                close();
                onChange({ title: `Add column ${c.name}?`, sql: addColumnSQL(engine, table, c) });
              }}
            />
          )}
        </FormDialog>
      )}
      {adding === "index" && (
        <FormDialog title="Add an index" onClose={() => setAdding(undefined)}>
          {(close) => (
            <IndexForm
              table={table}
              onSubmit={(name, cols, unique) => {
                close();
                onChange({
                  title: `Add index ${name}?`,
                  sql: addIndexSQL(engine, table, name, cols, unique),
                });
              }}
            />
          )}
        </FormDialog>
      )}
      {adding === "rename" && (
        <FormDialog title={`Rename ${table.name}`} onClose={() => setAdding(undefined)}>
          {(close) => (
            <NameForm
              label="New name"
              initial={table.name}
              onSubmit={(to) => {
                close();
                onChange({
                  title: `Rename ${table.name}?`,
                  sql: renameTableSQL(engine, table, to),
                });
              }}
            />
          )}
        </FormDialog>
      )}
    </div>
  );
}

function FormDialog({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: (close: () => void) => ReactNode;
}) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>You'll see the SQL before it runs.</DialogDescription>
        </DialogHeader>
        {children(onClose)}
      </DialogContent>
    </Dialog>
  );
}

const nameRe = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/;

function NameForm({
  label,
  initial = "",
  onSubmit,
}: {
  label: string;
  initial?: string;
  onSubmit: (name: string) => void;
}) {
  const [name, setName] = useState(initial);
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(name.trim());
      }}
    >
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="struct-name">{label}</Label>
        <Input
          id="struct-name"
          autoFocus
          className="font-mono"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </div>
      <DialogFooter>
        <Button type="submit" disabled={!nameRe.test(name.trim()) || name.trim() === initial}>
          Show the SQL
        </Button>
      </DialogFooter>
    </form>
  );
}

function ColumnFields({
  engine,
  c,
  set,
  idPrefix,
}: {
  engine: Engine;
  c: NewColumn;
  set: (c: NewColumn) => void;
  idPrefix: string;
}) {
  const types = typeSuggestions(engine);
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${idPrefix}-name`}>Name</Label>
        <Input
          id={`${idPrefix}-name`}
          className="font-mono"
          value={c.name}
          onChange={(e) => set({ ...c, name: e.target.value })}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${idPrefix}-type`}>Type</Label>
        <Input
          id={`${idPrefix}-type`}
          className="font-mono"
          list={`${idPrefix}-types`}
          value={c.type}
          onChange={(e) => set({ ...c, type: e.target.value })}
        />
        <datalist id={`${idPrefix}-types`}>
          {types.map((t) => (
            <option key={t} value={t} />
          ))}
        </datalist>
      </div>
      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label htmlFor={`${idPrefix}-default`}>Default</Label>
        <Input
          id={`${idPrefix}-default`}
          className="font-mono"
          placeholder="None; an SQL value, like 0 or 'text' or CURRENT_TIMESTAMP"
          value={c.default ?? ""}
          onChange={(e) => set({ ...c, default: e.target.value })}
        />
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          className="accent-primary"
          checked={c.nullable}
          onChange={(e) => set({ ...c, nullable: e.target.checked })}
        />
        Can be NULL
      </label>
    </div>
  );
}

function ColumnForm({ engine, onSubmit }: { engine: Engine; onSubmit: (c: NewColumn) => void }) {
  const [c, set] = useState<NewColumn>({
    name: "",
    type: typeSuggestions(engine)[0] ?? "",
    nullable: true,
  });
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit({ ...c, name: c.name.trim() });
      }}
    >
      <ColumnFields engine={engine} c={c} set={set} idPrefix="add-col" />
      <DialogFooter>
        <Button type="submit" disabled={!nameRe.test(c.name.trim()) || !c.type.trim()}>
          Show the SQL
        </Button>
      </DialogFooter>
    </form>
  );
}

function IndexForm({
  table,
  onSubmit,
}: {
  table: Table;
  onSubmit: (name: string, cols: string[], unique: boolean) => void;
}) {
  const [cols, setCols] = useState<string[]>([]);
  const [unique, setUnique] = useState(false);
  const [name, setName] = useState("");
  const shown = name || (cols.length ? `${table.name}_${cols.join("_")}_idx`.slice(0, 63) : "");
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(shown, cols, unique);
      }}
    >
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1.5 font-mono text-xs leading-none tracking-wide uppercase">
          Columns, in order
        </legend>
        {table.columns.map((c) => (
          <label key={c.name} className="flex items-center gap-2 font-mono text-sm">
            <input
              type="checkbox"
              className="accent-primary"
              checked={cols.includes(c.name)}
              onChange={(e) =>
                setCols(e.target.checked ? [...cols, c.name] : cols.filter((x) => x !== c.name))
              }
            />
            {c.name}
            {cols.includes(c.name) && (
              <span className="text-xs text-muted-foreground">{cols.indexOf(c.name) + 1}</span>
            )}
          </label>
        ))}
      </fieldset>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="index-name">Name</Label>
        <Input
          id="index-name"
          className="font-mono"
          placeholder={shown || "Picked from its columns"}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          className="accent-primary"
          checked={unique}
          onChange={(e) => setUnique(e.target.checked)}
        />
        Unique: no two rows may have the same values
      </label>
      <DialogFooter>
        <Button type="submit" disabled={cols.length === 0 || !nameRe.test(shown)}>
          Show the SQL
        </Button>
      </DialogFooter>
    </form>
  );
}

// NewTableDialog makes a table: an id column to start with (an auto
// numbered primary key), and any others.
export function NewTableDialog({
  engine,
  onSubmit,
  onClose,
}: {
  engine: Engine;
  onSubmit: (c: Change) => void;
  onClose: () => void;
}) {
  const intType = engine === "postgres" ? "bigint" : "BIGINT";
  const [name, setName] = useState("");
  const [cols, setCols] = useState<NewColumn[]>([
    { name: "id", type: intType, nullable: false, primary: true, auto: true },
    { name: "", type: typeSuggestions(engine)[2] ?? "", nullable: true },
  ]);
  const valid =
    nameRe.test(name.trim()) &&
    cols.every((c) => nameRe.test(c.name.trim()) && c.type.trim()) &&
    new Set(cols.map((c) => c.name.trim())).size === cols.length;

  function submit(e: FormEvent) {
    e.preventDefault();
    onSubmit({
      title: `Create table ${name.trim()}?`,
      sql: createTableSQL(
        engine,
        name.trim(),
        cols.map((c) => ({ ...c, name: c.name.trim() })),
      ),
    });
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <form onSubmit={submit} className="flex min-h-0 flex-col gap-4">
          <DialogHeader>
            <DialogTitle>New table</DialogTitle>
            <DialogDescription>
              It starts with an <code>id</code> that numbers rows by itself. You'll see the SQL
              before it runs.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="table-name">Name</Label>
            <Input
              id="table-name"
              autoFocus
              className="font-mono"
              placeholder="players"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className="flex max-h-[45vh] flex-col gap-3 overflow-y-auto pr-1">
            {cols.map((c, i) => (
              <div
                // biome-ignore lint/suspicious/noArrayIndexKey: columns are positional while edited
                key={i}
                className="flex flex-col gap-2 rounded-lg border p-3"
              >
                <div className="flex items-center justify-between">
                  <span className="text-xs text-muted-foreground">
                    Column {i + 1}
                    {c.primary ? " · primary key" : ""}
                    {c.auto ? ", numbered by itself" : ""}
                  </span>
                  {i > 0 && (
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label="Remove column"
                      onClick={() => setCols(cols.filter((_, j) => j !== i))}
                    >
                      <Trash2 />
                    </Button>
                  )}
                </div>
                <ColumnFields
                  engine={engine}
                  c={c}
                  idPrefix={`new-col-${i}`}
                  set={(n) => setCols(cols.map((x, j) => (j === i ? n : x)))}
                />
              </div>
            ))}
          </div>
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="self-start"
            onClick={() =>
              setCols([
                ...cols,
                { name: "", type: typeSuggestions(engine)[2] ?? "", nullable: true },
              ])
            }
          >
            <Plus /> Another column
          </Button>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!valid}>
              Show the SQL
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ChangeDialog shows a change's SQL and runs it.
export function ChangeDialog({
  nodeId,
  serverId,
  id,
  change,
  onDone,
  onClose,
}: {
  nodeId: string;
  serverId: string;
  id: string;
  change: Change;
  onDone: () => void;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run() {
    setBusy(true);
    setError("");
    try {
      await dbCommand(nodeId, serverId, "db.query", { id, sql: change.sql });
      await client.invalidateQueries({ queryKey: ["db-schema", nodeId, serverId, id] });
      await client.invalidateQueries({ queryKey: ["db-rows", nodeId, serverId, id] });
      onDone();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{change.title}</DialogTitle>
          <DialogDescription>
            {change.destructive ? "This can't be undone. It runs:" : "It runs:"}
          </DialogDescription>
        </DialogHeader>
        <pre className="max-h-64 overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs whitespace-pre-wrap">
          {change.sql}
        </pre>
        {change.confirmName && (
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="change-confirm">
              Type <span className="font-mono normal-case">{change.confirmName}</span> to confirm
            </Label>
            <Input
              id="change-confirm"
              autoComplete="off"
              spellCheck={false}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
            />
          </div>
        )}
        {error && <p className="font-mono text-sm whitespace-pre-wrap text-destructive">{error}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant={change.destructive ? "destructive" : "default"}
            disabled={busy || (!!change.confirmName && typed !== change.confirmName)}
            onClick={run}
          >
            {busy && <Loader2 className="animate-spin" />} Run it
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
