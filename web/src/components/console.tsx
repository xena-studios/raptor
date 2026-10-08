import "@xterm/xterm/css/xterm.css";

import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { type FormEvent, type KeyboardEvent, useEffect, useRef, useState } from "react";

import { Input } from "@/components/ui/input";
import { message } from "@/lib/errors";
import { live } from "@/lib/live";
import { commandClient } from "@/lib/transport";

// Console shows a server's live console (xterm.js renders the output, so
// it's never HTML) and, with console.write, a line to send commands.
// Loaded lazily: xterm is the app's biggest dependency.
export default function Console({
  nodeId,
  serverId,
  canWrite,
}: {
  nodeId: string;
  serverId: string;
  canWrite: boolean;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [problem, setProblem] = useState<{ text: string; retrying: boolean }>();
  const [command, setCommand] = useState("");
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);
  const history = useRef<string[]>([]);
  const back = useRef(-1);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const term = new Terminal({
      convertEol: true,
      disableStdin: true,
      scrollback: 5000,
      fontSize: 13,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
      theme: { background: "#0a0a0a" },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();
    const resize = new ResizeObserver(() => fit.fit());
    resize.observe(el);
    const stop = live.watchConsole(nodeId, serverId, {
      onLines: (lines, reset) => {
        if (reset) term.reset();
        setProblem(undefined);
        if (lines.length > 0) term.write(`${lines.join("\r\n")}\r\n`);
      },
      onProblem: (text, retrying) => setProblem({ text, retrying }),
    });
    return () => {
      stop();
      resize.disconnect();
      term.dispose();
    };
  }, [nodeId, serverId]);

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
    <div className="flex flex-col gap-2">
      <div className="relative overflow-hidden rounded-lg border bg-[#0a0a0a] p-2">
        <div ref={box} className="h-[28rem]" />
        {problem && (
          <div className="absolute inset-x-0 top-0 bg-background/90 px-3 py-1.5 text-xs text-muted-foreground">
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
