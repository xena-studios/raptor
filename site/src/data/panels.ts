// Raptor beside the other panels people run, for /compare/. The long
// version, every cell with its source, is docs-site's
// start/compare-panels page: change both together. Checked against each
// project's own docs, code, and releases on 2026-11-16. "None found" means
// we looked in its docs and code and found nothing, which isn't proof it's
// missing; say so rather than "No".
export const checked = "2026-11-16";

export const panels = ["Raptor", "Calagopus", "FeatherPanel", "Hydrodactyl", "Pelican", "Pterodactyl", "PufferPanel"] as const;

export const links: Record<(typeof panels)[number], string> = {
  Raptor: "https://github.com/xena-studios/raptor",
  Calagopus: "https://github.com/calagopus/panel",
  FeatherPanel: "https://github.com/MythicalLTD/FeatherPanel",
  Hydrodactyl: "https://github.com/BlueprintFramework/hydrodactyl",
  Pelican: "https://github.com/pelican/panel",
  Pterodactyl: "https://github.com/pterodactyl/panel",
  PufferPanel: "https://github.com/pufferpanel/pufferpanel",
};

/** A cell: its text, and whether it's a gap (shown quieter). */
export type Cell = string | { t: string; gap: true };
const gap = (t: string): Cell => ({ t, gap: true });

export type Row = { row: string; cells: Cell[] };
export type Group = { name: string; rows: Row[] };

export const groups: Group[] = [
  {
    name: "Running it",
    rows: [
      { row: "What it is", cells: ["A hosted panel; all of it open source", "Rewrite of Pterodactyl in Rust", "PHP panel; its daemon forks Wings", "Fork of Pterodactyl, via Pyrodactyl", "Fork of Pterodactyl", "The original", "Its own panel in Go"] },
      { row: "Who runs the panel", cells: ["We do", "You do", "You do, or their Cloud", "You do", "You do", "You do", "You do"] },
      { row: "To install the panel", cells: ["Nothing", "PostgreSQL and Valkey, by Docker or a binary", "PHP, MariaDB, Redis, by Docker Compose", "PHP 8.4, Composer, a database, Redis, a web server", "PHP 8.4, a web server, SQLite or a database", "PHP, Composer, MySQL, Redis, a web server, a queue worker", "One package or Docker"] },
      { row: "Ports to open on a node", cells: ["None: it connects out", gap("8080, 2022"), gap("8080, 2022"), gap("8080, 2022"), gap("8080, 2022"), gap("8080, 2022"), gap("8080, 5657")] },
      { row: "Server isolation", cells: ["Docker", "Docker", "Docker", "Docker", "Docker", "Docker", gap("Docker optional; none by default")] },
      { row: "Pterodactyl eggs", cells: ["Run as they are", "Run as they are", "Imported", "Run as they are", "Imported", "Its own format", gap("Own templates only")] },
      { row: "Move from Pterodactyl", cells: ["raptor import pterodactyl, beside it", "Built-in importer", "Importer and CLI", "Migrates in place", "Upgrades in place", "—", gap("No")] },
    ],
  },
  {
    name: "Games",
    rows: [
      { row: "Plugin and mod installer", cells: ["Built in: Modrinth, Hangar, CurseForge, uMod, Factorio", gap("Third-party extension"), gap("None found"), "Built in for Minecraft: Modrinth, Hangar, Spiget", "Free official plugin: Modrinth, uMod", gap("Paid add-ons"), gap("No")] },
      { row: "Modpacks", cells: ["Modrinth, CurseForge, FTB, Technic", gap("Third-party extension"), gap("None found"), gap("None found"), "Through the Modrinth plugin", gap("Paid add-ons"), "CurseForge, by template"] },
      { row: "Steam Workshop", cells: ["Built in", gap("None found"), gap("None found"), gap("No"), gap("No"), gap("Paid add-ons"), gap("No")] },
      { row: "Extensions", cells: [gap("None: features are built in"), "Its own extensions", "Plugins and a marketplace", gap("None"), "Plugins and a Plugin Hub", "Blueprint (third-party); official in 2.0 beta", gap("Themes only")] },
    ],
  },
  {
    name: "Data",
    rows: [
      { row: "Backups", cells: ["Incremental, deduplicated, encrypted", "Many drivers: dedup, Restic, Kopia, snapshots", gap("Full archives"), gap("Full archives"), gap("Full archives"), gap("Full archives"), gap("Full archive, server stopped")] },
      { row: "Backups go to", cells: ["The machine, S3, B2, SFTP, or ours", "The machine, S3, Restic, Kopia, PBS", "The machine, S3, PBS", "The machine, S3", "The machine, S3", "The machine, S3", gap("The machine only")] },
      { row: "Database engines", cells: ["MariaDB, MySQL, PostgreSQL, Redis, Valkey, MongoDB", "MySQL, MariaDB, PostgreSQL, Redis, MongoDB", "MySQL, MariaDB, PostgreSQL, Redis, MongoDB", gap("MySQL, MariaDB"), gap("MySQL, MariaDB"), gap("MySQL, MariaDB"), gap("None")] },
      { row: "Database browser", cells: ["Built in, for every engine", "Built in", "phpMyAdmin", gap("No"), gap("No"), gap("No"), gap("No")] },
      { row: "Disk limits", cells: ["Kernel quotas, always", "Quotas if set up; else scans", gap("Scans; quotas optional"), gap("Scans"), gap("Scans; quotas in progress"), gap("Scans"), gap("None found")] },
    ],
  },
  {
    name: "Safety",
    rows: [
      { row: "Passkeys", cells: ["Yes, and they sign dangerous actions", "Yes", "Yes", gap("No (authenticator apps)"), "Yes", gap("No (authenticator apps)"), gap("Authenticator apps")] },
      { row: "Alerts", cells: ["Email, Discord, Slack, Telegram, ntfy, webhooks", gap("None found"), "Email, webhooks", "Discord, Slack, Mattermost", "Webhooks; Discord by plugin", gap("None found"), gap("None found")] },
    ],
  },
  {
    name: "Cost and status",
    rows: [
      { row: "Licence", cells: ["AGPL-3.0", "MIT", "AGPL-3.0", "Apache-2.0", "AGPL-3.0", "MIT", "Apache-2.0"] },
      { row: "Price", cells: ["$12 a machine a month; first month free", "Free", "Free; paid Premium", "Free", "Free", "Free; add-ons paid", "Free"] },
      { row: "Latest release", cells: ["0.17, early access", "1.2.10", "1.5.4", "6.4.2, called pre-release", "1.0 release candidate", "1.15.2; 2.0 in beta", "3.0.9"] },
    ],
  },
];

export const text = (c: Cell) => (typeof c === "string" ? c : c.t);
export const isGap = (c: Cell) => typeof c !== "string";
