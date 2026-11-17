// What's new in Raptor, newest first: /changelog, and the latest few on the
// home page. Add an entry when a feature people would notice reaches
// production (CONTRIBUTING.md#the-site). Plain words, what you can now do,
// and only what's live: nothing in review, nothing announced ahead.
export type Change = {
  title: string;
  text: string;
  /** A docs path ("/servers/addons/") or a site path ("/library/"). */
  link?: string;
};
export type Release = { date: string; title: string; changes: Change[] };

export const changelog: Release[] = [
  {
    date: "2026-11-16",
    title: "Minecraft, all the way down",
    changes: [
      {
        title: "Mods and plugins from CurseForge",
        text: "The Add-ons tab now searches CurseForge beside Modrinth and Hangar, and installs a mod's dependencies with it.",
        link: "/servers/addons/",
      },
      {
        title: "One Add-ons tab, with updates",
        text: "Plugins, mods, datapacks, and Workshop items live on one tab, which says what's out of date and updates everything at once. Client-only mods are hidden, and dependencies come along.",
        link: "/servers/addons/",
      },
      {
        title: "Modpacks from a list",
        text: "Pick a Modrinth, Feed The Beast, or Technic modpack by name instead of pasting its ID.",
        link: "/servers/addons/",
      },
      {
        title: "Players, Worlds, and Game tabs",
        text: "See who's online, and manage the whitelist, operators, and bans. Edit game rules per world, and server.properties as a form. A new players permission lets moderators do just that.",
      },
      {
        title: "Switch versions and Java from a list",
        text: "Move a Minecraft server to another version without editing variables, and when the console says the Java is wrong, Raptor offers the one it needs.",
      },
      {
        title: "Bedrock players on Java servers",
        text: "Turn on Geyser and Bedrock players can join a Java server. Bedrock servers also get behaviour and resource packs.",
      },
      {
        title: "Proxies that know your servers",
        text: "Pick a Velocity, BungeeCord, or Waterfall proxy's backends from your own servers, instead of typing addresses.",
      },
      {
        title: "The server list entry, with a preview",
        text: "Upload an icon and style the MOTD with buttons instead of § codes, and see it as players will.",
        link: "/servers/settings/",
      },
      {
        title: "Rust plugins and Factorio mods",
        text: "Install Oxide and Carbon plugins from uMod on Rust servers, and mods from the Factorio mod portal.",
        link: "/servers/addons/",
      },
      {
        title: "Needs attention",
        text: "A server's overview lists what's wrong, worst first: from crashes to failed installs and low disk. Common console errors are named, with the fix.",
      },
      {
        title: "Share a log in one click",
        text: "Put the console or a log file on mclo.gs, with IP addresses hidden first, and send the link to whoever's helping.",
        link: "/servers/overview/",
      },
      {
        title: "Links to your apps' dashboards",
        text: "Servers with a web page of their own, like txAdmin or Grafana, get a button that opens it.",
      },
      {
        title: "Your own domain, with the records",
        text: "A dialog gives the DNS records to point your own domain at a server, and a guide for each provider.",
        link: "/help/your-own-domain/",
      },
    ],
  },
  {
    date: "2026-11-15",
    title: "The game library",
    changes: [
      {
        title: "Over 350 games and apps, ready to install",
        text: "Raptor's own library: Minecraft in every flavour, over a hundred Steam games, voice servers, bots, and self-hosted apps. Each one is tested on a real node, on Raptor's own pinned images, and kept up to date.",
        link: "/library/",
      },
      {
        title: "Popular and Trending",
        text: "The library opens on what people are running most, and what's picking up.",
        link: "/library/",
      },
      {
        title: "Worlds backed up whole",
        text: "Games that can pause saving do so around a backup, so a world is never caught mid-write.",
        link: "/servers/backups/",
      },
    ],
  },
  {
    date: "2026-11-14",
    title: "Faces and icons",
    changes: [
      { title: "Game icons everywhere", text: "Eggs and the servers made from them show the game's own icon." },
      { title: "Team logos and avatars", text: "Give your team a logo. Your avatar comes from Google, GitHub, or Discord when you sign in with them." },
    ],
  },
  {
    date: "2026-10-28",
    title: "Databases, grown up",
    changes: [
      {
        title: "A Databases section",
        text: "Every database instance on every node, with its own page: what it uses, uptime, queries a second, and the servers that share each database.",
        link: "/nodes/databases/",
      },
      {
        title: "Instance backups",
        text: "Back up a database instance on its own schedule, apart from the servers that use it.",
        link: "/nodes/databases/",
      },
      {
        title: "Valkey, and safer Redis",
        text: "Valkey beside Redis, and Redis reachable from outside only on a TLS port.",
        link: "/nodes/databases/",
      },
      {
        title: "Egg versions",
        text: "Pick which version of an egg a server runs, and move it to the newest when you're ready.",
        link: "/eggs/library/",
      },
    ],
  },
  {
    date: "2026-10-26",
    title: "Leaving is easy too",
    changes: [
      {
        title: "raptor uninstall",
        text: "Remove Raptor from a machine in one command. Your servers' files stay unless you say otherwise.",
        link: "/cli/reference/",
      },
    ],
  },
  {
    date: "2026-10-20",
    title: "A firewall you can read",
    changes: [
      {
        title: "The machine's firewall, in the app",
        text: "Open and close ports on the node like ufw, in Raptor's own table that Docker can't undo, signed with your passkey.",
        link: "/nodes/system/",
      },
    ],
  },
  {
    date: "2026-10-19",
    title: "Databases and networks",
    changes: [
      {
        title: "Six database engines",
        text: "MariaDB, MySQL, PostgreSQL, Redis, Valkey, and MongoDB on your node, with a browser for each, imports, exports, read-only users, and version changes.",
        link: "/nodes/databases/",
      },
      {
        title: "Private networks",
        text: "Put servers on networks of their own, with rules between them, so one community's servers can't see another's.",
        link: "/nodes/networks/",
      },
      {
        title: "Import from another host",
        text: "Bring a server's files over from any host with SFTP, and change a server's type without starting over.",
        link: "/servers/files/",
      },
    ],
  },
];

/** "November 16, 2026" */
export const longDate = (iso: string) =>
  new Date(`${iso}T12:00:00Z`).toLocaleDateString("en-US", { month: "long", day: "numeric", year: "numeric", timeZone: "UTC" });
