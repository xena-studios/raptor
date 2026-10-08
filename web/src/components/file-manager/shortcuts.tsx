import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);

// Keys are written with "Mod" (Cmd on macOS, Ctrl elsewhere) and "Alt"
// (Option on macOS); [mac, other] when they differ.
type Keys = string | [string, string];
type Shortcut = { press: Keys; what: string; note?: string };

const groups: { title: string; items: Shortcut[] }[] = [
  {
    title: "File",
    items: [
      { press: "Mod+S", what: "Save" },
      { press: "F1", what: "All commands", note: "Search every command the editor has." },
    ],
  },
  {
    title: "Editing",
    items: [
      { press: "Mod+Z", what: "Undo" },
      { press: ["Mod+Shift+Z", "Mod+Y"], what: "Redo" },
      { press: "Mod+X", what: "Cut line", note: "With nothing selected, cuts the whole line." },
      { press: "Mod+C", what: "Copy line", note: "With nothing selected, copies the whole line." },
      { press: "Mod+Shift+K", what: "Delete line" },
      { press: "Alt+↑ / Alt+↓", what: "Move line up or down" },
      { press: "Shift+Alt+↑ / Shift+Alt+↓", what: "Copy line up or down" },
      { press: "Mod+Enter", what: "Insert line below" },
      { press: "Mod+Shift+Enter", what: "Insert line above" },
      { press: "Mod+] / Mod+[", what: "Indent or outdent" },
      { press: "Mod+/", what: "Comment or uncomment lines" },
      {
        press: "Shift+Alt+F",
        what: "Format the file",
        note: "For formats the editor knows, like JSON.",
      },
    ],
  },
  {
    title: "Selection and cursors",
    items: [
      { press: "Mod+A", what: "Select all" },
      { press: "Mod+D", what: "Add the next match to the selection" },
      { press: "Mod+Shift+L", what: "Select every match" },
      {
        press: ["Mod+Alt+↑ / Mod+Alt+↓", "Ctrl+Alt+↑ / Ctrl+Alt+↓"],
        what: "Add a cursor above or below",
      },
      { press: "Alt+Click", what: "Add a cursor" },
      { press: "Shift+Alt+Drag", what: "Select a column" },
      { press: "Mod+U", what: "Undo the last cursor move" },
      { press: "Esc", what: "Back to one cursor" },
    ],
  },
  {
    title: "Find",
    items: [
      { press: "Mod+F", what: "Find" },
      { press: ["Mod+Alt+F", "Mod+H"], what: "Find and replace" },
      { press: ["Mod+G", "F3"], what: "Next match" },
      { press: ["Mod+Shift+G", "Shift+F3"], what: "Previous match" },
    ],
  },
  {
    title: "Moving around",
    items: [
      { press: "Ctrl+G", what: "Go to line" },
      { press: ["Mod+↑ / Mod+↓", "Mod+Home / Mod+End"], what: "Start or end of the file" },
      { press: "Mod+Shift+\\", what: "Jump to the matching bracket" },
      { press: ["Mod+Alt+[ / Mod+Alt+]", "Mod+Shift+[ / Mod+Shift+]"], what: "Fold or unfold" },
    ],
  },
];

function show(k: string): string {
  if (!mac) return k.replaceAll("Mod", "Ctrl");
  return k
    .replaceAll("Mod", "⌘")
    .replaceAll("Shift", "⇧")
    .replaceAll("Alt", "⌥")
    .replaceAll("Ctrl", "⌃")
    .replaceAll("Enter", "↩");
}

function KeyCaps({ press }: { press: Keys }) {
  const k = typeof press === "string" ? press : mac ? press[0] : press[1];
  return (
    <span className="flex flex-wrap items-center gap-1">
      {k.split(" / ").map((combo, i) => (
        <span key={combo} className="flex items-center gap-1">
          {i > 0 && <span className="text-xs text-muted-foreground">or</span>}
          {show(combo)
            .split("+")
            .map((part) => (
              <kbd
                key={part}
                className="min-w-6 rounded border bg-muted px-1.5 py-0.5 text-center font-mono text-xs"
              >
                {part}
              </kbd>
            ))}
        </span>
      ))}
    </span>
  );
}

export function ShortcutsDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
          <DialogDescription>
            The file editor's shortcuts, with {mac ? "macOS" : "Windows and Linux"} keys. Log files
            open read-only, so the editing ones don't apply to them.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-6">
          {groups.map((g) => (
            <section key={g.title}>
              <h3 className="mb-1 font-mono text-xs uppercase tracking-wide text-muted-foreground">
                {g.title}
              </h3>
              <ul className="divide-y border-t">
                {g.items.map((s) => (
                  <li key={s.what} className="grid grid-cols-[11rem_1fr] items-start gap-4 py-2">
                    <KeyCaps press={s.press} />
                    <div>
                      <p className="text-sm">{s.what}</p>
                      {s.note && <p className="text-xs text-muted-foreground">{s.note}</p>}
                    </div>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
