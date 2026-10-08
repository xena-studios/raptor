import { useSyncExternalStore } from "react";

// The theme: system (follows the OS), light, or dark. It's kept in
// localStorage, so the page starts in it (public/theme.js), and on the
// account, so it follows the user to other browsers.
export type Theme = "system" | "light" | "dark";

export const themes: Theme[] = ["system", "light", "dark"];

const key = "raptor.theme";
const listeners = new Set<() => void>();
const dark = window.matchMedia("(prefers-color-scheme: dark)");

export function isTheme(t: unknown): t is Theme {
  return themes.includes(t as Theme);
}

export function getTheme(): Theme {
  try {
    const t = localStorage.getItem(key);
    return isTheme(t) ? t : "system";
  } catch {
    return "system";
  }
}

function apply(t: Theme) {
  document.documentElement.classList.toggle(
    "dark",
    t === "dark" || (t === "system" && dark.matches),
  );
}

export function setTheme(t: Theme) {
  try {
    localStorage.setItem(key, t);
  } catch {
    // Private browsing: it lasts until the page closes.
  }
  apply(t);
  for (const l of listeners) l();
}

dark.addEventListener("change", () => apply(getTheme()));

export function useTheme(): Theme {
  return useSyncExternalStore((l) => {
    listeners.add(l);
    return () => listeners.delete(l);
  }, getTheme);
}
