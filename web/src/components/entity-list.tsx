import { Link, type LinkProps, useNavigate } from "@tanstack/react-router";
import { cn } from "cn";
import type { ReactNode } from "react";

import { StatusBadge } from "@/components/server-status";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { TableRow } from "@/components/ui/table";
import type { ServerStatus } from "@/lib/servers";

// The node and server lists share these: a card or a table row that opens
// the item wherever it's clicked, with its status in the same place.

type To = Pick<LinkProps, "to" | "params">;

// EntityCard is one item in a grid: its name and status, a line under it,
// and its facts. The whole card is the link.
export function EntityCard({
  link,
  title,
  status,
  subtitle,
  rows,
}: {
  link: To;
  title: string;
  status: ServerStatus;
  subtitle: ReactNode;
  rows: [string, ReactNode][];
}) {
  return (
    <Link
      {...link}
      className="group rounded-xl outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
    >
      <Card className="h-full transition-colors group-hover:bg-muted/40">
        <CardHeader>
          <CardTitle className="flex items-center justify-between gap-2">
            <span className="truncate">{title}</span>
            <StatusBadge status={status} />
          </CardTitle>
          <CardDescription className="truncate">{subtitle}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2 text-sm">
          {rows.map(([label, value]) => (
            <div key={label} className="flex min-w-0 items-center justify-between gap-3">
              <span className="shrink-0 text-muted-foreground">{label}</span>
              <span className="flex min-w-0 justify-end truncate">{value}</span>
            </div>
          ))}
        </CardContent>
      </Card>
    </Link>
  );
}

// LinkRow is a table row that opens the item wherever it's clicked. Its
// name cell keeps a real link, for the keyboard and "open in a new tab";
// clicks on buttons inside (copy, reveal) stay theirs.
export function LinkRow({ link, children }: { link: To; children: ReactNode }) {
  const navigate = useNavigate();
  return (
    <TableRow
      className={cn("cursor-pointer")}
      onClick={(e) => {
        if ((e.target as HTMLElement).closest("a, button")) return;
        navigate(link);
      }}
    >
      {children}
    </TableRow>
  );
}
