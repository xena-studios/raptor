---
title: Plugins, mods, and the Workshop
description: Install Minecraft plugins, mods, and datapacks from Modrinth and Hangar, and Steam Workshop items, from the app.
sidebar:
  order: 6
---

Servers whose game supports them get a page to find and install add-ons, without downloading anything yourself.

## Plugins, mods, and datapacks

Minecraft servers get a **Plugins**, **Mods**, or **Datapacks** tab, depending on what they run:

| Tab | For | From |
|---|---|---|
| **Plugins** | Paper, Purpur, Spigot, Bukkit, Folia, Velocity, BungeeCord, Waterfall, Sponge | Modrinth, or Hangar (PaperMC's repository) |
| **Mods** | Fabric, Forge, NeoForge, Quilt | Modrinth |
| **Datapacks** | Any Java server | Modrinth |

On the tab:

- **Search** shows what works with your server's Minecraft version (or any version, when it's set to `latest`).
- **Install** a version: the machine downloads it straight from Modrinth's or Hangar's servers and checks it against the hash they publish, so a file swapped on the way is refused.
- **Installed** lists what's there, named from Modrinth or Hangar, with an **Update** button when there's a newer version. Files neither knows show as added by hand.
- **Remove** moves a file to the server's [trash](/servers/files/#the-trash).

Restart the server for changes to take effect. Installing needs the **Change files** permission.

SpigotMC isn't offered: its downloads often go to other sites without a hash, so the machine couldn't check what it got. Install those by uploading the jar, or with [From URL](/servers/files/#download-from-a-link).

## Steam Workshop

Games that load Steam Workshop items themselves get a **Workshop** tab: Garry's Mod (a collection), ARK (mods), and Squad (items).

- Paste a Workshop link or ID: Raptor shows what it is, and refuses items for another game.
- For games that take a list, a collection's link adds its items.
- The page says whether a **restart** or a **reinstall** picks the change up.

The game downloads the items itself; Raptor only changes the setting that lists them. It needs the **Startup settings** permission.
