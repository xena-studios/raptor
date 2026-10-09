import { useSyncExternalStore } from "react";

// The file manager's settings, kept in this browser.
export type FileSettings = {
  // One click opens; Ctrl/Cmd or Shift-click selects. Off: double-click opens.
  singleClick: boolean;
  wordWrap: boolean;
  stickyScroll: boolean;
  hideIndentGuides: boolean;
  hideAutocomplete: boolean;
  // Save the open file a few seconds after typing stops.
  autoSave: boolean;
  // "auto" follows the app's theme.
  editorTheme: EditorTheme;
  treeOpen: boolean;
  // The format the last archive was made in.
  archiveFormat: ArchiveFormat;
};

export type ArchiveFormat = "zip" | "tar.gz" | "tar.zst" | "tar";

export type EditorTheme = "auto" | "dark" | "light" | "hc-black";

export const editorThemes: { value: EditorTheme; label: string }[] = [
  { value: "auto", label: "Match Raptor" },
  { value: "dark", label: "Dark" },
  { value: "light", label: "Light" },
  { value: "hc-black", label: "High contrast" },
];

const defaults: FileSettings = {
  singleClick: true,
  wordWrap: false,
  stickyScroll: false,
  hideIndentGuides: false,
  hideAutocomplete: false,
  autoSave: false,
  editorTheme: "auto",
  treeOpen: true,
  archiveFormat: "zip",
};

const key = "raptor.files";
const listeners = new Set<() => void>();
let cached: FileSettings | undefined;

function read(): FileSettings {
  if (cached) return cached;
  try {
    cached = { ...defaults, ...(JSON.parse(localStorage.getItem(key) ?? "{}") as object) };
  } catch {
    cached = defaults;
  }
  return cached;
}

export function setFileSettings(change: Partial<FileSettings>) {
  cached = { ...read(), ...change };
  try {
    localStorage.setItem(key, JSON.stringify(cached));
  } catch {
    // Private browsing: it lasts until the page closes.
  }
  for (const l of listeners) l();
}

export function useFileSettings(): FileSettings {
  return useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, read);
}

// Drafts: what's typed in a file and not saved yet, kept in this browser so
// a closed tab or a lost connection doesn't lose it.
const draftKey = (node: string, server: string, path: string) =>
  `raptor.draft:${node}:${server}:${path}`;

export function loadDraft(node: string, server: string, path: string): string | null {
  try {
    return localStorage.getItem(draftKey(node, server, path));
  } catch {
    return null;
  }
}

export function saveDraft(node: string, server: string, path: string, text: string | null) {
  try {
    if (text === null) localStorage.removeItem(draftKey(node, server, path));
    else localStorage.setItem(draftKey(node, server, path), text);
  } catch {
    // Full or unavailable: the draft just isn't kept.
  }
}
