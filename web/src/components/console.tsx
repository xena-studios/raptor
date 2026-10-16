import "@xterm/xterm/css/xterm.css";

import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { Maximize2, Minimize2 } from "lucide-react";
import { type FormEvent, type KeyboardEvent, useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { message } from "@/lib/errors";
import { live } from "@/lib/live";
import { commandClient } from "@/lib/transport";
import { cn } from "@/lib/utils";

// Console shows a server's live console (xterm.js renders the output, so
// it's never HTML) and, with console.write, a line to send commands.
// Loaded lazily: xterm is the app's biggest dependency.
export default function Console({
  nodeId,
  serverId,
  canWrite,
  support,
  fill,
}: {
  nodeId: string;
  serverId: string;
  canWrite: boolean;
  // Raptor's staff, under this support request.
  support?: string;
  // Fill the parent's height (a pop-out window) instead of a fixed one.
  fill?: boolean;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [problem, setProblem] = useState<{ text: string; retrying: boolean }>();
  const [command, setCommand] = useState("");
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);
  const history = useRef<string[]>([]);
  const back = useRef(-1);
  // Full screen covers the window (like the file manager's), keeping the
  // command line; Escape leaves it.
  const [full, setFull] = useState(false);
  useEffect(() => {
    if (!full) return;
    const onKey = (e: globalThis.KeyboardEvent) => e.key === "Escape" && setFull(false);
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [full]);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const term = new Terminal({
      convertEol: true,
      disableStdin: true,
      scrollback: 5000,
      fontSize: 13,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      // The terminal ink from index.css; xterm needs a colour, not a token.
      theme: { background: "#0a0c11" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();
    // xterm measures its font when set, so it switches to the app's mono
    // once that's loaded.
    const mono = '"JetBrains Mono Variable", ui-monospace, monospace';
    let gone = false;
    document.fonts
      .load(`13px ${mono}`)
      .then(() => {
        if (gone) return;
        term.options.fontFamily = mono;
        fit.fit();
      })
      .catch(() => {});
    const resize = new ResizeObserver(() => fit.fit());
    resize.observe(el);
    const stop = live.watchConsole(
      nodeId,
      serverId,
      {
        onLines: (lines, reset) => {
          if (reset) term.reset();
          setProblem(undefined);
          if (lines.length > 0) term.write(`${lines.join("\r\n")}\r\n`);
        },
        onProblem: (text, retrying) => setProblem({ text, retrying }),
      },
      support,
    );
    return () => {
      gone = true;
      stop();
      resize.disconnect();
      term.dispose();
    };
  }, [nodeId, serverId, support]);

  async function send(e: FormEvent) {
    e.preventDefault();
    const c = command.trim();
    if (!c) return;
    setSending(true);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        action: "server.command",
        serverId,
        paramsJson: JSON.stringify({ command: c }),
        support,
      });
      history.current = [c, ...history.current.filter((h) => h !== c)].slice(0, 50);
      back.current = -1;
      setCommand("");
    } catch (err) {
      setError(message(err));
    } finally {
      setSending(false);
    }
  }

  // Up and down walk through the commands sent from this page.
  function walk(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
    e.preventDefault();
    const i = Math.max(
      -1,
      Math.min(history.current.length - 1, back.current + (e.key === "ArrowUp" ? 1 : -1)),
    );
    back.current = i;
    setCommand(i < 0 ? "" : (history.current[i] ?? ""));
  }

  return (
    <div
      className={cn(
        "flex flex-col gap-2",
        fill && "h-full min-h-0",
        full && "fixed inset-0 z-50 bg-background p-4",
      )}
    >
      <div
        className={cn(
          "terminal relative overflow-hidden rounded-lg p-2",
          (full || fill) && "min-h-0 flex-1",
        )}
      >
        <div ref={box} className={full || fill ? "h-full" : "h-[28rem]"} />
        <Button
          size="icon-sm"
          variant="ghost"
          className="absolute top-2 right-2 z-10 text-zinc-400 hover:bg-white/10 hover:text-zinc-100"
          aria-label={full ? "Leave full screen" : "Full screen"}
          title={full ? "Leave full screen (Esc)" : "Full screen"}
          onClick={() => setFull(!full)}
        >
          {full ? <Minimize2 /> : <Maximize2 />}
        </Button>
        {problem && (
          <div className="absolute inset-x-0 top-0 border-b border-white/10 bg-terminal/90 px-3 py-1.5 font-mono text-xs text-zinc-400">
            {problem.text}
            {problem.retrying && " Trying again…"}
          </div>
        )}
      </div>
      {canWrite && (
        <form onSubmit={send} className="flex flex-col gap-1">
          <Input
            aria-label="Command"
            placeholder="Type a command and press Enter"
            autoComplete="off"
            spellCheck={false}
            maxLength={4096}
            className="font-mono"
            value={command}
            disabled={sending}
            onChange={(e) => setCommand(e.target.value)}
            onKeyDown={walk}
          />
          {error && <p className="text-sm text-destructive">{error}</p>}
        </form>
      )}
    </div>
  );
}
