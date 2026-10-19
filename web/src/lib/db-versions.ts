// A pure helper, apart from databases.ts so the test runner can load it.

// versionChange is how Wings moves an instance between versions, as
// internal/wings/databases decides: in place, by dump and restore (a new
// PostgreSQL major), or not at all (older versions can't read newer data).
export function versionChange(
  engine: "mariadb" | "mysql" | "postgres" | "redis" | "mongodb",
  from: string,
  to: string,
) {
  const a = from.split(".").map(Number);
  const b = to.split(".").map(Number);
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const d = (b[i] ?? 0) - (a[i] ?? 0);
    if (d < 0) return "older";
    if (d > 0) break;
  }
  // MongoDB goes up one major version at a time.
  if (engine === "mongodb" && (b[0] ?? 0) - (a[0] ?? 0) > 1) return "skip";
  return engine === "postgres" && a[0] !== b[0] ? "migrate" : "in-place";
}
