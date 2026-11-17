import { cn } from "cn";

// Keys as the user's keyboard labels them: "Mod" is ⌘ on macOS and Ctrl
// elsewhere, "Alt" is ⌥ on macOS.
export const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);

export function keyLabel(k: string): string {
  if (!mac) return k.replaceAll("Mod", "Ctrl");
  return k
    .replaceAll("Mod", "⌘")
    .replaceAll("Shift", "⇧")
    .replaceAll("Alt", "⌥")
    .replaceAll("Ctrl", "⌃");
}

// Keys shows a shortcut as key caps ("Mod+K" → ⌘ K).
export function Keys({ k, className }: { k: string; className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-0.5", className)}>
      {keyLabel(k)
        .split("+")
        .map((part) => (
          <kbd
            key={part}
            className="inline-flex h-5 min-w-5 items-center justify-center rounded border bg-muted px-1 font-mono text-[0.65rem] leading-none text-muted-foreground"
          >
            {part}
          </kbd>
        ))}
    </span>
  );
}

// typing says whether a key press is going into something that takes
// text (a field, the file editor, a terminal), so "/" types a slash there.
export function typing(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  if (target.closest(".monaco-editor, .xterm")) return true;
  if (target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return true;
  if (target instanceof HTMLInputElement) {
    return !["checkbox", "radio", "button", "submit", "reset", "range", "color", "file"].includes(
      target.type,
    );
  }
  return false;
}
