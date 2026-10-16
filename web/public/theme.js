// Applies the saved theme before the app loads, so a light-mode page doesn't
// flash dark (a file of its own: the CSP allows no inline scripts).
try {
  const t = localStorage.getItem("raptor.theme") || "system";
  const s = localStorage.getItem("raptor.theme-style") || "standard";
  const dark =
    t === "dark" || (t === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
  const c = document.documentElement.classList;
  c.toggle("dark", dark);
  c.toggle("contrast", s === "contrast");
  c.toggle("calm", s === "calm");
} catch {
  // No storage: the app applies the theme when it loads.
}
