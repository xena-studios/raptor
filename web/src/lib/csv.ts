// csvText is rows as CSV (RFC 4180), with the column names first.
export function csvText(result: {
  columns: { name: string }[];
  rows: (string | null)[][];
}): string {
  const cell = (v: string | null) => {
    if (v === null) return "";
    return /[",\n\r]/.test(v) ? `"${v.replaceAll('"', '""')}"` : v;
  };
  const lines = [result.columns.map((c) => cell(c.name)).join(",")];
  for (const r of result.rows) lines.push(r.map(cell).join(","));
  return `${lines.join("\r\n")}\r\n`;
}
