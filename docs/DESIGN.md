---
name: Raptor
description: The Console, a calm instrument panel for running game servers on your own machines
colors:
  signal-red: "oklch(0.66 0.19 25)"
  signal-red-daylight: "oklch(0.545 0.215 25)"
  on-signal: "oklch(0.17 0.03 25)"
  on-signal-daylight: "oklch(0.99 0.01 25)"
  ink: "oklch(0.145 0.012 25)"
  ink-raised: "oklch(0.185 0.013 25)"
  ink-sidebar: "oklch(0.125 0.01 25)"
  ink-muted: "oklch(0.225 0.014 25)"
  ink-rule: "oklch(0.3 0.012 25)"
  bone-text: "oklch(0.96 0.005 25)"
  ash-text: "oklch(0.68 0.011 25)"
  daylight: "oklch(0.975 0.004 25)"
  daylight-raised: "oklch(0.955 0.006 25)"
  daylight-muted: "oklch(0.92 0.008 25)"
  daylight-rule: "oklch(0.88 0.01 25)"
  graphite-text: "oklch(0.18 0.018 25)"
  slate-text: "oklch(0.46 0.013 25)"
  ok: "oklch(0.8 0.16 150)"
  ok-daylight: "oklch(0.51 0.12 150)"
  warn: "oklch(0.82 0.15 80)"
  warn-daylight: "oklch(0.52 0.11 70)"
  danger: "oklch(0.72 0.17 10)"
  danger-daylight: "oklch(0.53 0.2 10)"
  terminal-ink: "#0a0c11"
  series-blue: "#3987e5"
  series-orange: "#d95926"
typography:
  headline:
    fontFamily: "Hanken Grotesk Variable, system-ui, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 700
    lineHeight: 1.333
    letterSpacing: "-0.025em"
  title:
    fontFamily: "Hanken Grotesk Variable, system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 500
    lineHeight: 1.375
  body:
    fontFamily: "Hanken Grotesk Variable, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.43
  meta:
    fontFamily: "Hanken Grotesk Variable, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 400
    lineHeight: 1.33
  label:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "0.75rem"
    fontWeight: 400
    letterSpacing: "0.025em"
  button:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "0.75rem"
    fontWeight: 500
    letterSpacing: "0.05em"
  kicker:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "0.7rem"
    fontWeight: 400
    letterSpacing: "0.18em"
  readout:
    fontFamily: "JetBrains Mono Variable, ui-monospace, monospace"
    fontSize: "1.25rem"
    fontWeight: 500
    fontFeature: "tnum"
rounded:
  sm: "2.4px"
  md: "3.4px"
  lg: "4px"
  xl: "5.6px"
spacing:
  control: "8px"
  card: "16px"
  card-sm: "12px"
  stack: "24px"
  page: "24px"
components:
  button-primary:
    backgroundColor: "{colors.signal-red}"
    textColor: "{colors.on-signal}"
    typography: "{typography.button}"
    rounded: "{rounded.lg}"
    height: "32px"
    padding: "0 10px"
  button-outline:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.bone-text}"
    typography: "{typography.button}"
    rounded: "{rounded.lg}"
    height: "32px"
    padding: "0 10px"
  button-destructive:
    textColor: "{colors.danger}"
    typography: "{typography.button}"
    rounded: "{rounded.lg}"
    height: "32px"
  card:
    backgroundColor: "{colors.ink-raised}"
    textColor: "{colors.bone-text}"
    rounded: "{rounded.xl}"
    padding: "{spacing.card}"
  input:
    textColor: "{colors.bone-text}"
    typography: "{typography.body}"
    rounded: "{rounded.lg}"
    height: "32px"
    padding: "0 10px"
  status-chip:
    textColor: "{colors.ok}"
    typography: "{typography.label}"
  badge:
    textColor: "{colors.bone-text}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    height: "20px"
    padding: "0 6px"
  terminal:
    backgroundColor: "{colors.terminal-ink}"
    rounded: "{rounded.lg}"
    padding: "12px"
---

# Design System: Raptor

The tokens above are what `web/src/index.css` defines: "The Console" is the dark theme, "Daylight" the light one, and the `-daylight` keys are the light theme's values for the same roles. The theme follows the system unless the user picks one on their account. Each mode also comes in two other styles, set by a class on `html` (#290): **High contrast** (`.contrast`: white on near-black or the reverse, secondary text and borders that hold at least 7:1 and 3:1, a deeper or brighter red, the faint card rings turned into full borders, and a solid 2px focus ring offset from the control) and **Calm** (`.calm`: true greys with no lean toward the red, grey washes, muted states and chart colours, and a quieter red kept only for primary actions and danger). Both redefine the same tokens, so components never check the style. The brief behind all of this (who it's for, the voice, the principles) is in [PRODUCT.md](PRODUCT.md#design-brief). Design work goes through the vendored impeccable skill, which reads both files first ([CONTRIBUTING.md](../CONTRIBUTING.md#design-work)).

## 1. Overview

**Creative North Star: "The Console"**

Raptor is an instrument panel for people who don't speak Linux: a calm, precise control surface you can leave open all day and trust at a glance. It's not a toy and not terminal cosplay. The motifs (mono readouts, bracketed states, hairline rules) carry meaning, state and identity and data, never decoration. When in doubt, quieter and sharper.

The surface is deep ink with neutrals tinted toward the brand's red (hue 25), so the greys belong to Raptor rather than to a stock palette. One red lights up: primary actions, the current page, live readouts. Green and amber appear only to say how something is. Everything else is ink, rules, and type. Density is a working tool's density: tables with many rows, cards with many labelled facts, one column of settings.

Layout is plain and repeated. Pages sit in a 72rem column with 24px of padding, start with a header (a `//` kicker or a back link, the title, a sentence, the actions), and stack cards 24px apart. Detail pages put routed tabs under the header. Settings pages keep to a single 48rem column on every tab. Motion is 150 to 250ms, ease-out (`cubic-bezier(0.2, 0.8, 0.2, 1)`), and only conveys state; nothing choreographs a page load, and everything respects reduced motion.

What it rejects, in PRODUCT.md's words: developer-terminal cosplay, toy dashboards, generic SaaS, and over-confirmation.

**Key Characteristics:**
- Restrained: tinted ink neutrals and one red accent, under a tenth of any screen.
- Mono is the chassis: buttons, labels, table heads, states, and every number are JetBrains Mono.
- State is spelled out, `[ RUNNING ]`, never carried by colour alone.
- Sharp 4px corners and 1px rules; no shadow lifts a page surface.
- Machines talk on terminal ink, the same in both themes.

## 2. Colors: The Signal-on-Ink Palette

Warm-tinted ink with a single signal red, and a small state vocabulary that only ever means state.

### Primary
- **Signal Red** (`oklch(0.66 0.19 25)`; Daylight `oklch(0.545 0.215 25)`, deeper for contrast on light): primary buttons, the active nav item and tab underline, focus rings, the selection highlight at 30%. A wash of it (`oklch(0.28 0.075 25)`) marks the active sidebar item and flag badges.

### Neutral
- **Ink** (`oklch(0.145 0.012 25)`): the page.
- **Raised Ink** (`oklch(0.185 0.013 25)`): cards and popovers.
- **Sidebar Ink** (`oklch(0.125 0.01 25)`): the sidebar, a step darker than the page so navigation recedes.
- **Muted Ink** (`oklch(0.225 0.014 25)`): secondary buttons, table row hover, footers.
- **Rule** (`oklch(0.3 0.012 25)`): every border, divider, and input outline; a brighter rule (`oklch(0.42 0.014 25)`) for scrollbar hover.
- **Bone** (`oklch(0.96 0.005 25)`): text.
- **Ash** (`oklch(0.68 0.011 25)`): secondary text, descriptions, table heads; at least 4.5:1 on Raised Ink.
- Daylight inverts the ladder: Daylight `oklch(0.975 0.004 25)` page, `oklch(0.955 0.006 25)` cards, `oklch(0.88 0.01 25)` rules, Graphite `oklch(0.18 0.018 25)` text, Slate `oklch(0.46 0.013 25)` secondary text.

### State
- **OK** (`oklch(0.8 0.16 150)`; Daylight `oklch(0.51 0.12 150)`): running, connected, done.
- **Warn** (`oklch(0.82 0.15 80)`; Daylight `oklch(0.52 0.11 70)`): starting, installing, attention needed.
- **Danger** (`oklch(0.72 0.17 10)`; Daylight `oklch(0.53 0.2 10)`): failed, crashed, destructive actions.
- Each has a wash (`ok-wash`, `warn-wash`, `danger-wash`) for banners: a tinted fill with a 30 to 40% border of the same hue.
- **Graph series**: blue `#3987e5` and orange `#d95926` (Daylight `#2a78d6`, `#eb6834`), validated against the card in each theme.
- **Terminal Ink** (`#0a0c11`): the console, logs, install commands, and egg scripts, in both themes. Its text is `oklch(0.92 0.008 250)`.

### Named Rules
**The One Signal Rule.** Red is for the action you'd take, the place you are, and what's live. A red that decorates (an icon, a heading, a "certified" tick) is a bug.

**The State-Only Rule.** Green, amber, and danger mean state and nothing else. Palette colours (`emerald-500`, `amber-500`, `sky-500`) are prohibited; use `text-ok`, `text-warn`, `text-destructive`, and the washes.

## 3. Typography

**Body Font:** Hanken Grotesk (with system-ui)
**Label/Mono Font:** JetBrains Mono (with ui-monospace)

**Character:** a quiet grotesque for what people read, and a mono for what machines report. The contrast between them is the hierarchy: sentences are sans, data and controls are mono. Both are self-hosted, and font files are never inlined as `data:` URIs, because the CSP only allows fonts from Raptor itself.

### Hierarchy
- **Headline** (700, 1.5rem, tracking -0.025em): page titles only, one per page.
- **Title** (500, 1rem): card titles and the sidebar wordmark.
- **Body** (400, 0.875rem, 1.43): descriptions, table cells, form values. Prose stays under 75ch.
- **Meta** (400, 0.75rem): helper text under fields, timestamps, details lines in the activity log.
- **Label** (JetBrains Mono 400, 0.75rem, +0.025em, uppercase): form labels and table heads.
- **Button** (JetBrains Mono 500, 0.75rem, +0.05em, uppercase): every button except text links.
- **Kicker** (JetBrains Mono 400, 0.7rem, +0.18em, uppercase): the `// dev org` line above a page title and the sidebar's `// manage`.
- **Readout** (JetBrains Mono 500, 1.25rem, tabular figures): stat tile values, load averages, storage used.

### Named Rules
**The Machine Voice Rule.** Every number is mono with tabular figures: ports, IPs, IDs, versions, sizes, percentages, counts, cron expressions. Every sentence is sans. A switch row's title is a sentence (sans, 500), not a field label.

**The One Kicker Rule.** A `//` kicker names the context (the org, "account", "Raptor admin") and appears at most once per page. Kickers above every card are prohibited.

## 4. Elevation

Flat. Depth comes from tone and 1px rules: the sidebar is darker than the page, cards are a step lighter than it, and every surface is framed by a hairline (`ring-1` of the foreground at 10%, or the Rule colour). Page surfaces never cast shadows.

### Shadow Vocabulary
- **Floating layer** (the menu and popover defaults): dropdowns, popovers, sheets, and dialogs keep a soft shadow, because they genuinely sit above the page.
- **Key highlight** (`box-shadow: inset 0 1px 0 oklch(1 0 0 / 0.22)`): the top edge of a primary button, like light on a metal key. The only highlight in the system.

### Named Rules
**The Hairline Rule.** If a card needs a shadow to separate from the page, its tone is wrong. Fix the tone, never add the shadow.

## 5. Components

### Buttons
- **Character:** small, mono, confident; they read like keys on a panel.
- **Shape:** 4px corners, 32px tall (28px small, 24px extra-small), 10px side padding, 6px icon gap.
- **Primary:** Signal Red with its key highlight, for the one main action in view.
- **Outline:** a Rule border on the page colour, for everything secondary in a header (Start, Restart, Stop; Export, Edit).
- **Ghost:** no frame, for actions inside rows and toolbars.
- **Destructive:** tinted, never solid: Danger text on a 10% Danger fill (20% in the dark theme).
- **States:** hover deepens the fill; focus is a 3px ring of the red at 50%; pressing nudges it down 1px; disabled is 50% opacity; busy swaps the icon for a spinner. A button that can't do anything right now is disabled, not hidden: Start is off while a server runs, Stop while it's stopped.

### Status Chips
- **Style:** `[ RUNNING ]` in the Label style, coloured by tone: OK for live, Warn for pending, Danger for failed, Ash for stopped, Ash at 60% for stale (the node is offline, so it's the last report).
- **Use:** every state of a server, node, backup, or egg. They never pulse; a page of breathing chips is noise.

### Badges
- **Style:** 2.4px corners, 20px tall, Label type. The default is a red wash; outline and secondary for quieter flags.
- **Use:** flags and ownership (`OFFICIAL`, `SHARED`, `DEFAULT`, `THIS NODE'S DISK`), never state.

### Cards / Containers
- **Corner Style:** 5.6px.
- **Background:** Raised Ink, framed by a 10% foreground ring.
- **Internal Padding:** 16px (12px for the small size), 16px between title and content.
- **Footer:** a Rule top border on Muted Ink at 50%.
- Never nested. A card's title says what it holds ("Name and limits", "Remove node"), never the tab's name again.

### Inputs / Fields
- **Style:** 32px tall, 4px corners, a Rule outline on a transparent fill (Muted Ink at 30% in the dark theme), body type; labels above in the Label style.
- **Focus:** the outline turns red with a 3px ring of the red at 50%.
- **Error / Disabled:** a Danger outline and ring; disabled at 50% opacity.
- Checkboxes and switches are the app's own components, never the browser's.

### Tables
- Heads in the Label style on Ash; cells 12px by 10px; rows divided by Rules and tinted on hover. Numeric cells are mono and tabular, right-aligned. Row actions are one visible action and a "…" menu for the rest. On phones, secondary columns hide rather than push the actions off-screen.

### Stat Tiles
- A small card: a Label-style name with its icon, a Readout value, a Meta line, and an optional usage bar or sparkline. The sparkline yields width to the value; the value never truncates.

### Navigation
- **Sidebar:** Sidebar Ink. The red mark and wordmark at the top, the org switcher (orgs, then "Raptor admin" for staff), the `// manage` kicker, sections with their nodes and servers folded under them (a small status dot each, the one place a dot is allowed), the `[ N ONLINE ] / M NODES` readout, and the user's menu. The active item gets the red wash and red text. Collapsed, it's icons with tooltips; on phones it's a sheet that closes on navigation.
- **Tabs:** sans body text, Ash at rest, Bone and a 2px red underline when active.

### Terminal
- **Style:** Terminal Ink, 4px corners, a cool 1px border, 12px padding, JetBrains Mono at 0.75rem (13px in the live console). Banners on it use its own colours, never the page's.
- **Use:** the live console (xterm), Wings' log, install commands, egg install scripts.

### Sign-in Pages
- **Layout (`AuthLayout`):** on wide screens, a Sidebar Ink panel on the left with the raptor and wordmark, one plain sentence about Raptor, and three hairline-ruled facts it can stand behind; the form on the right in a 22rem column, headed by a sans title and a line of Ash. On phones, the mark, then the form near the top. No card around the form: the page is the surface.
- **Order:** the passkey first in red (the one primary), then each provider as an outline button with its mark in the text's colour, then an `or by email` rule and the email form. Codes (email, authenticator) are one mono field, centred and tracked wide. Errors go above the buttons in the destructive alert.

### The Node Readout
- `[ 1 ONLINE ] / 9 NODES` at the foot of the sidebar: the online count in OK when any node is up, breathing (opacity 1 to 0.55 over 1.4s); the only ambient motion in the app, off under reduced motion.

## 6. Do's and Don'ts

### Do:
- **Do** keep Signal Red under a tenth of any screen: the primary action, where you are, what's live.
- **Do** spell every state in words inside a chip: `[ STOPPED ]`, not a grey dot.
- **Do** set every number in JetBrains Mono with tabular figures.
- **Do** write dates as "Oct 12, 2026, 9:00 PM" (no seconds) and recent ones as "5 minutes ago"; one formatter, everywhere.
- **Do** say schedules the way people do ("Every day at 4:00 AM"), with the cron expression beside it in mono.
- **Do** put destructive actions in their own card at the bottom of a page, with a sentence on what's lost, and ask once.
- **Do** disable an action that can't do anything right now, and say why when it isn't obvious ("The node is offline").
- **Do** name things the way owners think: server, node, egg, port; never container, image, or bind mount.

### Don't:
- **Don't** do developer-terminal cosplay: no neon, no glow, no Matrix green. Mono and ink carry meaning, not "hacker" decoration.
- **Don't** build toy dashboards: no big hero numbers, no confetti, no rounded pills everywhere.
- **Don't** drift into generic SaaS: no soft drop shadows on cards, no lavender gradients, no friendly blobs, no glassmorphism.
- **Don't** over-confirm: no dialog for every action. Inline first; confirm only what's destructive or irreversible.
- **Don't** show developer text: no raw action names, IDs, node error strings, or permission identifiers. If the app has no words for something, it says "made a change", not `egg customize: 01a1…`.
- **Don't** ship a tab for a feature that doesn't exist yet.
- **Don't** use palette colours (`emerald-500`, `amber-500`, `sky-500`) or hardcoded hex outside the terminal.
- **Don't** round past 6px on any surface or use pill-shaped badges.
- **Don't** put a kicker above every section, or repeat the page's name as a card's title.
- **Don't** use a gradient on text, a border-left stripe as an accent, or a shadow to lift a card.

## 7. Brand

The marks are in [`web/src/components/logo.tsx`](../web/src/components/logo.tsx); the site's header (`site/src/components/Logo.astro`) and both favicons are copies of them, so change them together.

- **The full logo** is the running raptor in Signal Red beside the **RAPTOR** wordmark: Archivo ExtraBold (800) at width 118 with 0.06em tracking, outlined to a path so no font loads for it. Wherever the wordmark appears, the raptor is beside it, and the raptor never appears without the wordmark: the site's header, the docs site's header (`docs-site/src/assets/logo-dark.svg` and `-light.svg`, with a mono **DOCS** label after a hairline), the full-page error screens, and the expanded sidebar. The raptor is wide, so it never appears alone at small sizes.
- **The small mark** is an R whose leg is a raptor's sickle claw, in Signal Red on its own: the collapsed sidebar, and anywhere 32px or more with no room for the full logo. It's never beside the wordmark. **The favicons** put the same R in Ink on a Signal Red tile with 6px corners, because at 16px a bare letter turns into a thin mark among the other tabs, and the tile stays a solid shape; app icons and avatars use the tile too.
- Both are one flat colour each: no gradients, outlines, or shadows. The raptor, the small mark, and the favicon's tile are Signal Red (its Daylight value in the light theme) and the wordmark is the text colour, Bone or Graphite.
- Archivo is only the wordmark's letters. The app and the site keep Hanken Grotesk and JetBrains Mono.
