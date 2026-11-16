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
| **Plugins** | Paper, Purpur, Spigot, Bukkit, Folia, Velocity, BungeeCord, Waterfall, Sponge | Modrinth, Hangar (PaperMC's repository), or CurseForge* |
| **Mods** | Fabric, Forge, NeoForge, Quilt | Modrinth, or CurseForge* |
| **Datapacks** | Any Java server | Modrinth |

\* CurseForge shows up once the Panel has a CurseForge API key. Some CurseForge authors only allow downloads from CurseForge itself: for those, Raptor links to the file to download by hand (then upload it in Files) instead of installing it.

On the tab:

- **Search** shows what works with your server's Minecraft version (or any version, when it's set to `latest`). Mods that only run on players' computers (shaders, minimaps, HUDs) are left out, since on a server they crash it or do nothing; **Show client-only mods** brings them back.
- **Install** a version: the machine downloads it straight from Modrinth's or Hangar's servers and checks it against the hash they publish, so a file swapped on the way is refused. If it needs other mods or plugins (most Fabric mods need Fabric API), the dialog lists them and installs them first; optional ones it works with can be ticked. If it can't run beside something you have installed, it says so before installing. To install it alone, tick **Install only this**.
- An installed add-on missing something it needs shows **Needs another add-on**, with a button to install what's missing. Removing an add-on that others need asks first.
- **Installed** lists what's there, named from Modrinth or Hangar. Each one says whether it's **up to date** for your server's game version, or has an **update available** with a button to install it. The top of the list counts the updates, and **Update all** installs every one after showing you each change. Files neither knows show as added by hand.
- **Remove** moves a file to the server's [trash](/servers/files/#the-trash).

Restart the server for changes to take effect. Installing needs the **Change files** permission.

SpigotMC isn't offered: its downloads often go to other sites without a hash, so the machine couldn't check what it got. Install those by uploading the jar, or with [Upload → From a link](/servers/files/#download-from-a-link).

## Copy add-ons between servers

Running a lobby and several game servers with the same plugins, or the same mods on a test and a live server? Each section's **⋯** menu has:

- **Export list** saves the installed add-ons as a small JSON file: each one by where it's from, its project, and its version, plus the files you added by hand (by name and hash).
- **Import list** takes such a file (choose it or paste it) and shows what it would change first: what it installs, what changes version, what it removes, what's already there, and the hand-added files to copy yourself. Nothing changes until you press **Apply**.

Applying looks every add-on up again for this server, and the machine checks each download against the hash its source publishes, as with any install. It also installs what they need that the list doesn't have. Removing is a switch (on by default): turn it off to only add, and removed files go to the [trash](/servers/files/#the-trash). Files added by hand on this server are never removed.

If a listed version doesn't run on this server's game version, it's skipped and named; turn on **use the newest for this server** to install the newest one instead. The file format is in [EGGS.md](https://github.com/xena-studios/raptor/blob/main/docs/EGGS.md#add-on-lists), for keeping lists in Git.

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

## Bedrock players (Geyser)

Java servers running Paper (and Purpur, Folia, Spigot), Velocity, Waterfall, Fabric, or NeoForge can let **Bedrock** players join: phones, consoles, and Windows. On **Network**, under **Bedrock players**:

1. **Let Bedrock players join** installs [Geyser](https://geysermc.org) and, unless you untick it, Floodgate (so Bedrock players don't need a Java account). Each is checked against its source's hash like any add-on. On Velocity and Waterfall, Floodgate is left out: on a proxy it also needs its key on every server behind it.
2. **Restart the server:** Geyser makes its settings on its first start.
3. **Finish setting up** puts Bedrock on the server's own port, so Bedrock players use the same address and port as Java players. (Raptor publishes every port for both TCP and UDP; Bedrock uses UDP.)

The card then shows the address and port to give Bedrock players. If they can't connect, check your provider's firewall lets **UDP** through on that port.

## Bedrock packs

Bedrock servers (Mojang's server, and Endstone) get a **Packs** section in Add-ons for behaviour and resource packs.

- **Add a pack** takes a `.mcpack` or a `.mcaddon` (which holds several). Raptor reads each pack's manifest, puts it in `behavior_packs/` or `resource_packs/`, and turns it on for the loaded world. Restart the server to load it.
- Start the server once first: the world has to exist before packs can be turned on for it.
- Packs already in the folders show up too: turn them on or off for the world, or remove them (the folder goes to the trash).
- A pack marked **Needs Beta APIs** uses scripts; turn on the world's Beta APIs experiment (in the world's settings in the game, then upload the world) or it won't run.

Raptor puts resource packs on the world's list, but players only download them if `texturepack-required=true` is set in `server.properties`.

## Steam Workshop

Games that load Steam Workshop items themselves get a **Workshop** section in Add-ons: Garry's Mod (a collection), Counter-Strike 2 (a map collection), ARK (mods), Conan Exiles (mods, loaded in the order you list them), and Squad (items).

- Paste a Workshop link or ID: Raptor shows what it is, and refuses items for another game.
- For games that take a list, a collection's link adds its items.
- The page says whether a **restart** or a **reinstall** picks the change up.

The game downloads the items itself; Raptor only changes the setting that lists them. It needs the **Startup settings** permission.
