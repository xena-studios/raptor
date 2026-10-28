// What Wings' node.db.usage returns (internal/wings/actions/databases.go):
// each running instance's latest sample, taken every 15 seconds, and its
// last 10 minutes of them.

export type DBUsagePoint = {
  at: string;
  memory_bytes: number;
  connections: number;
  queries_per_second?: number; // none until two samples of the same run
};

export type DBUsage = {
  at: string;
  memory_bytes: number;
  memory_limit_bytes: number;
  disk_bytes: number;
  disk_limit_bytes: number; // 0 = none
  connections: number;
  max_connections: number;
  uptime_seconds?: number;
  queries_per_second?: number;
  recent: DBUsagePoint[];
};

type Engine = "mariadb" | "mysql" | "postgres" | "redis" | "valkey" | "mongodb";

// formatQueries is a rate of queries: tenths under 10, then whole, then
// thousands.
export function formatQueries(q: number): string {
  if (q < 10) return `${q.toFixed(1)}/s`;
  if (q < 1000) return `${Math.round(q)}/s`;
  return `${(q / 1000).toFixed(q < 10_000 ? 1 : 0)}k/s`;
}

// queriesNote is what each engine counts as a query (Wings' readStatus).
export function queriesNote(e: Engine): string {
  switch (e) {
    case "postgres":
      return "Transactions a second";
    case "redis":
    case "valkey":
      return "Commands a second";
    case "mongodb":
      return "Operations a second";
  }
  return "Statements a second";
}

// fraction is how full something is, or nothing without a limit.
export function fraction(n: number, limit: number): number | undefined {
  return limit > 0 ? n / limit : undefined;
}
