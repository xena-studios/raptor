import { Search } from "lucide-react";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  loadMemory,
  type Memory,
  recordUse,
  saveMemory,
  togglePin as toggle,
} from "@/lib/command-recents";
import { rootStack, type Stack } from "@/lib/command-steps";

import { Keys, keyLabel, typing } from "./keys";
import { MenuBody } from "./menu";

// The command menu (#436, docs/PANEL.md#command-menu): every page and
// action a user can reach, found by typing. Ctrl+K or ⌘K opens it from
// anywhere, as does / when nothing takes text, and the search button in
// the top bar.

type Api = { open: () => void };

const MenuContext = createContext<Api | null>(null);

export function useCommandMenu(): Api {
  const c = useContext(MenuContext);
  if (!c) throw new Error("useCommandMenu needs a CommandMenuProvider");
  return c;
}

function storage(): Storage | undefined {
  try {
    return window.localStorage;
  } catch {
    return undefined;
  }
}

// CommandMenuProvider holds the menu for the signed-in pages: its hotkeys,
// its memory of what was used and pinned, and the menu itself.
export function CommandMenuProvider({
  orgId,
  children,
}: {
  // The org the menu searches: the page's, or the last one opened.
  orgId?: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [help, setHelp] = useState(false);
  const [stack, setStack] = useState<Stack>(rootStack);
  const [memory, setMemory] = useState<Memory>(() => loadMemory(storage()));
  const depth = useRef(1);
  depth.current = stack.length;
  // Focus goes back where it was when the menu is dismissed, but not when
  // it closes to run something: a page it opened, or a confirm it asks.
  const returnFocus = useRef(true);

  const show = useCallback(() => {
    returnFocus.current = true;
    setStack(rootStack);
    setOpen(true);
  }, []);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.defaultPrevented || e.isComposing) return;
      const mod = e.metaKey || e.ctrlKey;
      if (mod && !e.altKey && !e.shiftKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        if (open) setOpen(false);
        else show();
        return;
      }
      if (e.key === "/" && !mod && !e.altKey && !open && !typing(e.target)) {
        // Not over another dialog or menu, where / means nothing.
        const active = document.activeElement;
        if (active?.closest('[role="dialog"], [role="menu"], [role="listbox"]')) return;
        e.preventDefault();
        show();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, show]);

  const remember = useCallback((id: string) => {
    setMemory((m) => {
      const next = recordUse(m, id, Date.now());
      saveMemory(storage(), next);
      return next;
    });
  }, []);
  const togglePin = useCallback((id: string) => {
    setMemory((m) => {
      const next = toggle(m, id);
      saveMemory(storage(), next);
      return next;
    });
  }, []);

  const api = useMemo(() => ({ open: show }), [show]);

  return (
    <MenuContext.Provider value={api}>
      {children}
      <Dialog
        open={open}
        onOpenChange={(next, details) => {
          // Escape goes back a step first; at the start it closes.
          if (!next && details.reason === "escape-key" && depth.current > 1) {
            details.cancel();
            setStack((s) => s.slice(0, -1));
            return;
          }
          setOpen(next);
        }}
      >
        <DialogContent
          showCloseButton={false}
          finalFocus={() => returnFocus.current}
          aria-label="Command menu"
          className="top-[min(12vh,6rem)] flex max-h-[min(36rem,calc(100dvh-6rem))] translate-y-0 flex-col gap-0 overflow-hidden p-0 sm:max-w-xl data-open:slide-in-from-top-2"
        >
          <DialogTitle className="sr-only">Command menu</DialogTitle>
          <MenuBody
            orgId={orgId}
            stack={stack}
            setStack={setStack}
            memory={memory}
            remember={remember}
            togglePin={togglePin}
            close={() => {
              returnFocus.current = false;
              setOpen(false);
            }}
            openHelp={() => setHelp(true)}
          />
        </DialogContent>
      </Dialog>
      <ShortcutsHelp open={help} onOpenChange={setHelp} />
    </MenuContext.Provider>
  );
}

// CommandMenuButton opens the menu: a search field's look in the top bar,
// with its shortcut, and just the icon on phones.
export function CommandMenuButton() {
  const { open } = useCommandMenu();
  return (
    <Button
      variant="outline"
      size="sm"
      onClick={open}
      aria-label="Search and run commands"
      aria-keyshortcuts={keyLabel("Mod+K").replace("⌘", "Meta+").replace("Ctrl", "Control")}
      title={`Search and run commands (${keyLabel("Mod+K")})`}
      className="h-8 gap-2 px-2 font-sans normal-case tracking-normal text-muted-foreground sm:w-56 sm:justify-start"
    >
      <Search className="size-4" />
      <span className="hidden flex-1 text-left text-sm sm:inline">Search…</span>
      <Keys k="Mod+K" className="hidden sm:inline-flex" />
    </Button>
  );
}

export const menuShortcuts: { keys: string[]; what: string }[] = [
  { keys: ["Mod+K"], what: "Open or close the command menu, from anywhere" },
  { keys: ["/"], what: "Open it when you're not typing in a field" },
  { keys: ["↑", "↓"], what: "Move through the results" },
  { keys: ["Enter"], what: "Open or run the highlighted result" },
  { keys: ["Tab"], what: "A server's, node's, or egg's own commands" },
  { keys: ["Backspace"], what: "Back a step, when the search is empty" },
  { keys: ["Esc"], what: "Back a step; at the start, close the menu" },
  { keys: ["Alt+P"], what: "Pin or unpin the highlighted result" },
  { keys: ["?"], what: "These shortcuts, when the search is empty" },
];

const appShortcuts: { keys: string[]; what: string }[] = [
  { keys: ["Mod+B"], what: "Show or hide the sidebar" },
  { keys: ["↑", "↓"], what: "Earlier commands, in a server's console" },
];

// ShortcutsHelp lists the menu's keys and the app's others.
export function ShortcutsHelp({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const section = (title: string, list: { keys: string[]; what: string }[]) => (
    <section>
      <h3 className="mb-1 font-mono text-xs tracking-wide text-muted-foreground uppercase">
        {title}
      </h3>
      <ul className="divide-y border-t">
        {list.map((s) => (
          <li key={s.what} className="grid grid-cols-[7rem_1fr] items-center gap-4 py-2">
            <span className="flex flex-wrap items-center gap-1">
              {s.keys.map((k) => (
                <Keys key={k} k={k} />
              ))}
            </span>
            <span className="text-sm">{s.what}</span>
          </li>
        ))}
      </ul>
    </section>
  );
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
          <DialogDescription>
            The command menu finds every page, server, and action you can use. The file editor has
            its own shortcuts, listed in the file manager's settings.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-6">
          {section("Command menu", menuShortcuts)}
          {section("Elsewhere", appShortcuts)}
        </div>
      </DialogContent>
    </Dialog>
  );
}
