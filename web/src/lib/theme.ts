import { useSyncExternalStore } from "react";

// The theme is a mode, system (follows the OS), light, or dark, and a
// style: standard, high contrast, or calm (neutral greys; red only where
// it means something). Both are kept in localStorage, so the page starts
// in them (public/theme.js), and on the account, so they follow the user
// to other browsers.
export type Theme = "system" | "light" | "dark";
export type ThemeStyle = "standard" | "contrast" | "calm";

const themes: Theme[] = ["system", "light", "dark"];
const styles: ThemeStyle[] = ["standard", "contrast", "calm"];

const key = "raptor.theme";
const styleKey = "raptor.theme-style";
const listeners = new Set<() => void>();
const dark = window.matchMedia("(prefers-color-scheme: dark)");

export function isTheme(t: unknown): t is Theme {
  return themes.includes(t as Theme);
}

export function isThemeStyle(s: unknown): s is ThemeStyle {
  return styles.includes(s as ThemeStyle);
}

function read<T>(k: string, ok: (v: unknown) => v is T, fallback: T): T {
  try {
    const v = localStorage.getItem(k);
    return ok(v) ? v : fallback;
  } catch {
    return fallback;
  }
}

function write(k: string, v: string) {
  try {
    localStorage.setItem(k, v);
  } catch {
    // Private browsing: it lasts until the page closes.
  }
}

export function getTheme(): Theme {
  return read(key, isTheme, "system");
}

export function getThemeStyle(): ThemeStyle {
  return read(styleKey, isThemeStyle, "standard");
}

function apply() {
  const t = getTheme();
  const s = getThemeStyle();
  const c = document.documentElement.classList;
  c.toggle("dark", t === "dark" || (t === "system" && dark.matches));
  c.toggle("contrast", s === "contrast");
  c.toggle("calm", s === "calm");
}

function changed() {
  apply();
  for (const l of listeners) l();
}

export function setTheme(t: Theme) {
  write(key, t);
  changed();
}

export function setThemeStyle(s: ThemeStyle) {
  write(styleKey, s);
  changed();
}

dark.addEventListener("change", apply);

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useTheme(): Theme {
  return useSyncExternalStore(subscribe, getTheme);
}

export function useThemeStyle(): ThemeStyle {
  return useSyncExternalStore(subscribe, getThemeStyle);
}
