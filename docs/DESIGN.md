# Design: "The Console"

The web app's visual system. "The Console" is the dark theme and "Daylight" the light one; the theme follows the system unless the user picks one in account settings. Tokens live in [`web/src/index.css`](../web/src/index.css); the brief behind them (register, users, voice, principles) is at the end of [PRODUCT.md](PRODUCT.md#design-brief). It's adapted from the earlier Raptor panel's design.

## North star

An instrument panel for people who don't speak Linux: a calm, precise control surface, not a toy and not terminal cosplay. The motifs (mono readouts, bracket chips, hairline rules) carry meaning (state, identity, data), never decoration. When in doubt, quieter and sharper.

## Colour

OKLCH, with every neutral leaning toward the red (hue 25) so the greys belong to the brand. Never pure black or white. **Restrained:** tinted neutrals plus one red accent (`--primary`) for primary actions, the active nav item, and live readouts; a small set of state colours for state only.

| Role | Console (`.dark`) | Daylight (`:root`) |
| --- | --- | --- |
| `--background` | `oklch(0.145 0.012 25)` | `oklch(0.975 0.004 25)` |
| `--foreground` | `oklch(0.96 0.005 25)` | `oklch(0.18 0.018 25)` |
| `--card` | `oklch(0.185 0.013 25)` | `oklch(0.955 0.006 25)` |
| `--primary` | `oklch(0.66 0.19 25)` | `oklch(0.545 0.215 25)` |
| `--muted-foreground` | `oklch(0.68 0.011 25)` | `oklch(0.46 0.013 25)` |
| `--border` | `oklch(0.3 0.012 25)` | `oklch(0.88 0.01 25)` |

**State:** `text-ok` / `bg-ok`, `text-warn` / `bg-warn`, `text-destructive`, and their washes for banners (`bg-ok-wash`, `bg-warn-wash`, `bg-danger-wash`, `bg-brand-wash`). Never a palette colour like `emerald-500` or `amber-500`. `--rule-bright` is a brighter hairline. Graphs use `--series-1` and `--series-2`, validated against the card in each theme.

**Terminal ink:** `--color-terminal` (`#0a0c11`) and the `.terminal` utility: a deep cool surface, dark in both themes, for the console (xterm reads the same colour), logs, install commands, and egg scripts.

## Type

- **Hanken Grotesk** (`--font-sans`, `--font-heading`): headings, body, nav, descriptions.
- **JetBrains Mono** (`--font-mono`) is the chassis, not just for code: buttons, form labels, table headers, status chips, badges, stat labels and values, and data (IDs, ports, IPs, versions, counts, sizes), with `tabular-nums`.
- Both are self-hosted (fontsource), and font files are never inlined as `data:` URIs, because the CSP allows fonts from `'self'` only (`web/vite.config.ts`).
- Fixed rem scale. Page titles `text-2xl font-bold tracking-tight`; card titles `text-base font-medium`; body `text-sm`; meta `text-xs`.
- Labels and table heads: mono, uppercase, `tracking-wide` / `tracking-wider`. Buttons the same at `text-xs`, except `link` buttons, which stay in sentence case.

## Shape and depth

- **Sharp:** `--radius: 0.25rem`; `rounded-lg` and `rounded-xl` do most of the work. Badges are `rounded-sm`. Nothing pill-soft except avatars.
- **Hairlines, not shadows:** surfaces are framed by `ring-1 ring-foreground/10` or `border`; dividers are `border-b` / `divide-y`. Floating layers (menus, popovers, sheets) keep a shadow so they read as above the page. The one other light effect is the 1px top highlight on primary buttons.
- Thin scrollbars in the border colour; selection is the red at 30%.

## Motion

150–250 ms, ease-out (`--ease-out-quint`), no bounce, no page-load choreography. The one ambient motion is `live-pulse` (opacity 1 → 0.55 over 1.4 s), only on the sidebar's live node count, and off under reduced motion. No glow, ever.

## Motifs

1. **Bracket status chips** `[ RUNNING ]`: `StatusBadge` in `web/src/components/server-status.tsx`, mono, uppercase, coloured by the tone from `web/src/lib/servers.ts` (live → `ok`, pending → `warn`, failed → `destructive`, stopped → muted, stale → muted and dimmed). The way state is shown for servers and nodes. They don't pulse; a list of breathing chips is noise.
2. **`// label` kickers:** `PageHeader`'s `eyebrow` (`web/src/components/page.tsx`) and the sidebar's `// manage`. They name the context (the org, "account", "Raptor admin"), never repeat the title, and appear on a few pages, not above every section.
3. **The node readout:** `[ 3 ONLINE ] / 4 NODES` at the bottom of the sidebar, the only thing that breathes.
4. **Terminal ink** for anything that's a machine talking.

## Components

Every interactive component has its default, hover, focus, active, disabled, loading, and error states, and looks the same on every page.

- **Buttons** (`ui/button.tsx`): default is the red with the highlight; `outline`, `secondary`, `ghost`; `destructive` is tinted, not solid red. `active:translate-y-px`.
- **Badges** (`ui/badge.tsx`): for flags and ownership (`OFFICIAL`, `SHARED`, `DRAFT`), not state, which uses chips. The default badge is a red wash, so a flag never outshouts an action.
- **Cards** (`ui/card.tsx`): `rounded-xl bg-card ring-1`, footers `border-t bg-muted/50`. Never nested.
- **Tables** (`ui/table.tsx`): mono uppercase muted heads, `px-3 py-2.5` cells, hairline rows, numeric cells mono and `tabular-nums`.
- **Stat tiles** (`StatCard` in `stats.tsx`): mono label, mono value. Not hero numbers.
- **Empty states** (`EmptyState`): a dashed border, a bare muted icon, a title, a sentence that teaches, and the action.
- **Sidebar** (`app-shell.tsx`): the red mark and wordmark, the org switcher, `// manage`, the node readout, the user's menu.

## Bans

No glow or neon. No shadow as elevation for page surfaces. No hero numbers or gradient text. No dot-and-label statuses where a chip fits (the sidebar's lists keep a small dot, which is all the room there is). No pill-soft badges. No palette colours where a state token exists. No filled icon bubbles. Mono is structure, never decoration.
