---
title: Plugins, mods, and the Workshop
description: Install Minecraft plugins, mods, and datapacks from Modrinth and Hangar, and Steam Workshop items, from the app.
sidebar:
  order: 6
---

Servers whose game supports them get a page to find and install add-ons, without downloading anything yourself.

## Plugins, mods, and datapacks

Servers whose game takes add-ons get an **Add-ons** tab, next to Files. It has a section for each kind the game takes (switch between them at its top). Minecraft servers get **Plugins**, **Mods**, or **Datapacks**, depending on what they run:

| Tab | For | From |
|---|---|---|
| **Plugins** | Paper, Purpur, Spigot, Bukkit, Folia, Velocity, BungeeCord, Waterfall, Sponge | Modrinth, or Hangar (PaperMC's repository) |
| **Mods** | Fabric, Forge, NeoForge, Quilt | Modrinth |
| **Datapacks** | Any Java server | Modrinth |

On the tab:

- **Search** shows what works with your server's Minecraft version (or any version, when it's set to `latest`). Mods that only run on players' computers (shaders, minimaps, HUDs) are left out, since on a server they crash it or do nothing; **Show client-only mods** brings them back.
- **Install** a version: the machine downloads it straight from Modrinth's or Hangar's servers and checks it against the hash they publish, so a file swapped on the way is refused. If it needs other mods or plugins (most Fabric mods need Fabric API), the dialog lists them and installs them first; optional ones it works with can be ticked. If it can't run beside something you have installed, it says so before installing. To install it alone, tick **Install only this**.
- An installed add-on missing something it needs shows **Needs another add-on**, with a button to install what's missing. Removing an add-on that others need asks first.
- **Installed** lists what's there, named from Modrinth or Hangar. Each one says whether it's **up to date** for your server's game version, or has an **update available** with a button to install it. The top of the list counts the updates, and **Update all** installs every one after showing you each change. Files neither knows show as added by hand.
- **Remove** moves a file to the server's [trash](/servers/files/#the-trash).

Restart the server for changes to take effect. Installing needs the **Change files** permission.

SpigotMC isn't offered: its downloads often go to other sites without a hash, so the machine couldn't check what it got. Install those by uploading the jar, or with [Upload → From a link](/servers/files/#download-from-a-link).

## Factorio mods

Factorio servers get a **Mods** section for mods from the [Factorio mod portal](https://mods.factorio.com), for the server's Factorio version, most downloaded first.

- Set **your factorio.com username and token** in Startup first (the token is on your factorio.com profile page): the portal only lets accounts that own Factorio download mods. Raptor's node adds them to each download itself.
- **Install** brings the mods it needs with it (and offers the optional ones), and says when it can't run beside one you have. Each download is checked against the portal's hash.
- **Installed** names mods by their file and offers updates for your Factorio version. Factorio turns on every mod in the folder; to turn one off without removing it, use the game's mod settings or `mods/mod-list.json`.

## Rust plugins (uMod)

Rust servers get a **Plugins** tab for Oxide and Carbon plugins from [uMod](https://umod.org). Set **Mod framework** to Oxide or Carbon in Startup first (vanilla Rust doesn't load plugins); the tab puts plugins in the folder that framework reads (`oxide/plugins` or `carbon/plugins`).

- uMod only offers each plugin's newest version, so that's what's installed, checked against the hash uMod lists.
- **Installed** names plugins by their file (`Kits.cs` is Kits) and offers an update when yours isn't uMod's newest. Plugins you changed yourself look out of date too.
- Plugins' settings are in `oxide/config` (or `carbon/configs`) in the file manager.
- Codefling and other paid plugin sites aren't offered: their downloads need a login. Upload those in the file manager.

## Steam Workshop

Games that load Steam Workshop items themselves get a **Workshop** section in Add-ons: Garry's Mod (a collection), Counter-Strike 2 (a map collection), ARK (mods), Conan Exiles (mods, loaded in the order you list them), and Squad (items).

- Paste a Workshop link or ID: Raptor shows what it is, and refuses items for another game.
- For games that take a list, a collection's link adds its items.
- The page says whether a **restart** or a **reinstall** picks the change up.

The game downloads the items itself; Raptor only changes the setting that lists them. It needs the **Startup settings** permission.
