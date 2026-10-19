import { cn } from "cn";
import { ArrowDown, ArrowUp } from "lucide-react";

import type { Result } from "@/lib/db-browser";

// Grid shows rows: a header that sorts (when onSort is given), NULL told
// apart from text, long values cut to a line, and a checkbox per row when
// rows can be picked.
export function Grid({
  result,
  sort,
  desc,
  onSort,
  selected,
  onSelect,
  onOpen,
  keyOf,
}: {
  result: Result;
  sort?: string;
  desc?: boolean;
  onSort?: (column: string) => void;
  // Picking rows (to delete them), by their key.
  selected?: Set<string>;
  onSelect?: (key: string, on: boolean) => void;
  // Opening a row (to see or change all of it).
  onOpen?: (row: (string | null)[]) => void;
  keyOf?: (row: (string | null)[]) => string;
}) {
  if (result.columns.length === 0) return null;
  const picking = !!(selected && onSelect && keyOf);
  const allOn =
    picking && result.rows.length > 0 && result.rows.every((r) => selected.has(keyOf(r)));
  return (
    <div className="overflow-auto rounded-lg border">
      <table className="w-full border-collapse text-sm">
        <thead className="sticky top-0 z-10 bg-muted">
          <tr>
            {picking && (
              <th className="w-8 border-b px-2 py-1.5">
                <input
                  type="checkbox"
                  aria-label="Pick every row"
                  className="accent-primary"
                  checked={allOn}
                  onChange={(e) => {
                    for (const r of result.rows) onSelect(keyOf(r), e.target.checked);
                  }}
                />
              </th>
            )}
            {result.columns.map((c) => (
              <th
                key={c.name}
                scope="col"
                className="border-b px-3 py-1.5 text-left font-mono text-xs font-normal whitespace-nowrap text-muted-foreground"
              >
                {onSort ? (
                  <button
                    type="button"
                    className="inline-flex items-center gap-1 hover:text-foreground"
                    onClick={() => onSort(c.name)}
                  >
                    {c.name}
                    {sort === c.name &&
                      (desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />)}
                  </button>
                ) : (
                  c.name
                )}
                {c.type && <span className="ml-1.5 opacity-60">{c.type}</span>}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {result.rows.map((row, i) => {
            const key = keyOf ? keyOf(row) : String(i);
            return (
              <tr
                key={key}
                className={cn(
                  "border-b last:border-b-0",
                  onOpen && "cursor-pointer hover:bg-muted/50",
                  picking && selected.has(key) && "bg-brand-wash",
                )}
                onClick={() => onOpen?.(row)}
              >
                {picking && (
                  <td className="px-2 py-1">
                    <input
                      type="checkbox"
                      aria-label="Pick this row"
                      className="accent-primary"
                      checked={selected.has(key)}
                      onClick={(e) => e.stopPropagation()}
                      onChange={(e) => onSelect(key, e.target.checked)}
                    />
                  </td>
                )}
                {row.map((v, j) => (
                  <td
                    // biome-ignore lint/suspicious/noArrayIndexKey: columns are positional
                    key={j}
                    className="max-w-[24rem] truncate px-3 py-1 font-mono text-[0.8rem]"
                    title={v ?? "NULL"}
                  >
                    {v === null ? <span className="text-muted-foreground italic">NULL</span> : v}
                  </td>
                ))}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
