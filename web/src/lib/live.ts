import { apiURL } from "@/lib/transport";

// The tab's one live connection to the Panel (internal/panel/live): every
// console the tab shows is a watch on it. It reconnects by itself, with
// backoff, and starts every watch again; each start sends the console's
// history with reset set.

type In = { op: "console"; id: string; node: string; server: string } | { op: "close"; id: string };

type Out = {
  id: string;
  type: "lines" | "ended";
  lines?: string[];
  reset?: boolean;
  error?: string;
  retry?: boolean;
};

export type ConsoleHandlers = {
  // reset: the console starts over with its history; clear what's shown.
  onLines: (lines: string[], reset: boolean) => void;
  // The watch can't go on: retrying says it's being tried again.
  onProblem: (message: string, retrying: boolean) => void;
};

type Watch = { node: string; server: string; h: ConsoleHandlers; retry?: number };

function liveURL(): string {
  const u = new URL(`${apiURL}/live`, window.location.href);
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  return u.toString();
}

class Live {
  private ws?: WebSocket;
  private open = false;
  private watches = new Map<string, Watch>();
  private backoff = 1000;
  private next = 0;

  watchConsole(node: string, server: string, h: ConsoleHandlers): () => void {
    const id = `c${++this.next}`;
    this.watches.set(id, { node, server, h });
    this.connect();
    if (this.open) this.send({ op: "console", id, node, server });
    return () => {
      const w = this.watches.get(id);
      if (w?.retry) clearTimeout(w.retry);
      this.watches.delete(id);
      if (this.open) this.send({ op: "close", id });
      if (this.watches.size === 0) this.ws?.close();
    };
  }

  private send(m: In) {
    this.ws?.send(JSON.stringify(m));
  }

  private connect() {
    if (this.ws) return;
    const ws = new WebSocket(liveURL());
    this.ws = ws;
    ws.onopen = () => {
      this.open = true;
      this.backoff = 1000;
      for (const [id, w] of this.watches)
        this.send({ op: "console", id, node: w.node, server: w.server });
    };
    ws.onmessage = (e) => {
      let m: Out;
      try {
        m = JSON.parse(String(e.data)) as Out;
      } catch {
        return;
      }
      const w = this.watches.get(m.id);
      if (!w) return;
      if (m.type === "lines") {
        w.h.onLines(m.lines ?? [], !!m.reset);
        return;
      }
      // Ended: the node went offline (try again), or this can't be watched.
      w.h.onProblem(m.error || "The console stream ended.", !!m.retry);
      if (m.retry) {
        w.retry = window.setTimeout(() => {
          if (this.watches.get(m.id) === w && this.open)
            this.send({ op: "console", id: m.id, node: w.node, server: w.server });
        }, 3000);
      }
    };
    ws.onclose = () => {
      this.ws = undefined;
      this.open = false;
      if (this.watches.size === 0) return;
      for (const w of this.watches.values()) w.h.onProblem("Reconnecting to the Panel…", true);
      setTimeout(() => this.connect(), this.backoff);
      this.backoff = Math.min(this.backoff * 2, 30_000);
    };
  }
}

export const live = new Live();
