// The connection test's verdict (docs/PANEL.md#connection-test): what the
// node says the game listens on (server.ports), and what the Panel saw
// from outside (TestConnection), into one answer per port. Kept free of
// imports so `node --test` can check it.

export type Socket = { port: number; proto: "tcp" | "udp"; loopback?: boolean };

export type Inside = {
  state: string;
  // Whether the node could read what the game listens on.
  checked: boolean;
  listening: Socket[];
};

export type Outside = {
  port: number;
  primary: boolean;
  reachable: boolean;
  failure: string; // "refused", "timeout", "closed", "local_only", "unknown", or ""
};

export type Cause =
  | "ok" // players can reach it
  | "ok_udp" // the game listens on UDP; TCP can't tell, but nothing is wrong inside
  | "not_running" // start the server first
  | "starting" // running, but nothing listens yet
  | "wrong_port" // the game listens on other ports
  | "loopback" // the game listens on 127.0.0.1 only
  | "blocked" // listens fine inside; dropped or refused outside: a firewall
  | "local_only" // the allocation is 127.0.0.1 on purpose
  | "unknown"; // couldn't tell

export type Verdict = { port: number; primary: boolean; cause: Cause; others: number[] };

// diagnose decides each port. inside is undefined when the node couldn't
// say (an older Wings, or it's offline): then only the outside counts.
export function diagnose(outside: Outside[], inside: Inside | undefined): Verdict[] {
  return outside.map((o) => {
    const base = { port: o.port, primary: o.primary, others: [] as number[] };
    if (o.failure === "local_only") return { ...base, cause: "local_only" };
    if (o.reachable) return { ...base, cause: "ok" };
    if (!inside) return { ...base, cause: o.failure === "timeout" ? "blocked" : "unknown" };
    if (inside.state !== "running" && inside.state !== "starting") {
      return { ...base, cause: "not_running" };
    }
    if (!inside.checked) return { ...base, cause: o.failure === "timeout" ? "blocked" : "unknown" };
    const here = inside.listening.filter((s) => s.port === o.port);
    const open = here.filter((s) => !s.loopback);
    if (open.some((s) => s.proto === "tcp")) {
      // Hung up at once though the game listens: the forwarder in front of
      // it can't reach it, which a firewall doesn't explain.
      return { ...base, cause: o.failure === "closed" ? "unknown" : "blocked" };
    }
    // UDP only: the Panel's TCP probe can't reach it either way.
    if (open.some((s) => s.proto === "udp")) return { ...base, cause: "ok_udp" };
    if (here.length > 0) return { ...base, cause: "loopback" };
    const others = [
      ...new Set(inside.listening.filter((s) => !s.loopback && s.port >= 1024).map((s) => s.port)),
    ].filter((p) => !outside.some((x) => x.port === p));
    if (others.length > 0) return { ...base, cause: "wrong_port", others };
    return { ...base, cause: "starting" };
  });
}
