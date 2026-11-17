---
title: Add-ons
description: Install plugins and mods from Modrinth, Hangar, CurseForge, Thunderstore, and more, Steam Workshop items and Arma Reforger mods, and Counter-Strike 2's plugin frameworks, from the app.
sidebar:
  order: 4
---

Servers whose game supports them get an **Add-ons** tab to find, install, and update plugins, mods, and the like, without downloading anything yourself.

## Plugins, mods, and datapacks

The **Add-ons** tab sits after Files. It has a section for each kind the game takes (switch between them at its top). Minecraft Java servers get **Plugins**, **Mods**, or **Datapacks**, depending on what they run:

| Section | For | From |
|---|---|---|
| **Plugins** | Paper, Purpur, Folia, Spigot, Bukkit | Modrinth, Hangar (PaperMC's repository), or CurseForge* |
| **Plugins** | Velocity, BungeeCord, Waterfall | Modrinth or Hangar |
| **Plugins** | Sponge | Modrinth |
| **Mods** | Fabric, Forge, NeoForge, Quilt | Modrinth, or CurseForge* |
| **Datapacks** | Any Java server | Modrinth |

When there's more than one source, buttons at the top of the search pick which one to search.

\* CurseForge shows up once the Panel has a CurseForge API key. Some CurseForge authors only allow downloads from CurseForge itself: for those, Raptor offers **Download on CurseForge** (or **Get … on CurseForge** for an update) instead of installing it; download the file, then upload it in Files.

Seeing the tab needs the **See files** permission.

On the tab:

- **Search** shows what works with your server's Minecraft version (or any version, when it's set to `latest`). On Modrinth, mods that only run on players' computers (shaders, minimaps, HUDs) are left out, since on a server they crash it or do nothing; **Show client-only mods** brings them back.
- **Install** a version: the machine downloads it straight from its source (Modrinth, Hangar, or CurseForge) and checks it against the hash the source publishes, so a file swapped on the way is refused. If it needs other mods or plugins (most Fabric mods need Fabric API), the dialog lists them under **Also installs what it needs** and the button becomes **Install with N more**; optional ones it works with can be ticked. If it can't run beside something you have installed, it says so before installing. To install it alone, tick **Install only this**.
- An installed add-on missing something it needs shows **Needs another add-on**, with **Install what it needs**. Removing an add-on that others need asks first.
- **Installed** lists what's there, named from its source. Each one says whether it's **Up to date** for your server's game version, or has an **Update available** with a button to install it. The top of the list counts the updates, and **Update all** installs every one after showing you each change. Files no source knows show as **Added by hand**.
- **Remove** moves a file to the server's [trash](/servers/files/#the-trash).

Restart the server for changes to take effect. Installing needs the **Change files** permission.

SpigotMC isn't offered: its downloads often go to other sites without a hash, so the machine couldn't check what it got. Install those by uploading the jar, or with [Upload → From a link](/servers/files/#download-from-a-link).

## Copy add-ons between servers

Running a lobby and several game servers with the same plugins, or the same mods on a test and a live server? The **⋯** menu beside a section's installed list has:

- **Export list** saves the installed add-ons as a small JSON file: each one by where it's from, its project, and its version, plus the files you added by hand (by name and hash).
- **Import list** takes such a file (choose it or paste it) and shows what it would change first: what it installs, what changes version, what it removes, what's already there, and the hand-added files to copy yourself. Nothing changes until you press **Apply N changes**.

Applying looks every add-on up again for this server, and the machine checks each download against the hash its source publishes, as with any install. It also installs what they need that the list doesn't have. Removing is a switch (on by default): turn it off to only add, and removed files go to the [trash](/servers/files/#the-trash). Files added by hand on this server are never removed.

If a listed version doesn't run on this server's game version, it's skipped and named; turn on **Where a listed version can't be installed here, use the newest for this server** to install the newest one instead. The file format is in [EGGS.md](https://github.com/xena-studios/raptor/blob/main/docs/EGGS.md#add-on-lists), for keeping lists in Git.

## Factorio mods

Factorio servers get a **Mods** section for mods from the [Factorio mod portal](https://mods.factorio.com), for the server's Factorio version, most downloaded first.

- Set **Factorio username** and **Factorio token** in Startup first (the token is on your factorio.com profile page): the portal only lets accounts that own Factorio download mods. Raptor's node adds them to each download itself.
- **Install** brings the mods it needs with it (and offers the optional ones), and says when it can't run beside one you have. Each download is checked against the portal's hash.
- **Installed** names mods by their file and offers updates for your Factorio version. Factorio turns on every mod in the folder; to turn one off without removing it, use the game's mod settings or `mods/mod-list.json`.

## Valheim and V Rising mods (Thunderstore)

Valheim and V Rising servers get a **Mods** section for BepInEx mods from [Thunderstore](https://thunderstore.io), most downloaded first. Set **Mod loader** to BepInEx in Startup and reinstall the server first (on Valheim, Valheim Plus works too): the reinstall installs BepInEx, which loads the mods.

- **Install** brings the mods it needs with it, each at its newest version. BepInEx itself isn't listed: the server already has it. Mods marked **Client-only** only run on players' computers.
- Thunderstore doesn't publish hashes, so the Panel downloads the mod first and hashes it; the machine then downloads the same file from Thunderstore and keeps it only if it matches.
- Each mod is unpacked into its own folder in `BepInEx/plugins`. **Remove** moves that folder to the trash; an update replaces it, with the old one in the trash.
- Mods' settings are in `BepInEx/config` in the file manager, made when the server first starts with the mod.
## Unturned plugins (OpenMod)

:::note
Coming with the next update of the Unturned egg: the plugin installer is in, but the egg that turns it on is still being tested.
:::

Unturned servers get a **Plugins** tab for [OpenMod](https://openmod.github.io/openmod-docs/) plugins, which are published on [NuGet](https://www.nuget.org/packages?q=Tags%3A%22openmod-plugin%22).

- Turn on **OpenMod** in Startup and restart first. Servers made before OpenMod was in the Unturned egg need a reinstall to download it (saves and configs stay). It can run beside RocketMod.
- **Install** brings the plugins it needs with it. Each package is checked against the hash NuGet lists, and only the plugin itself is copied into `Servers/Raptor/OpenMod/plugins`, named after the package and version (`Kr4ken.NewEssentials@1.5.1.dll`).
- Some plugins need libraries Raptor can't install (ones made of several files). The install dialog names them: run `openmod install <name>` in the console and OpenMod installs them itself.
- **Installed** names the plugins Raptor installed and offers updates. DLLs you uploaded yourself show as added by hand; plugins from `openmod install` live in OpenMod's own `packages` folder and are managed with its commands.
- RocketMod's plugins aren't offered: there's no central site with hashes to check them against. Upload those to `Servers/Raptor/Rocket/Plugins` in the file manager.

## Rust plugins (uMod)

Rust servers get a **Plugins** section for Oxide and Carbon plugins from [uMod](https://umod.org). Set **Mod framework** to Oxide or Carbon in Startup first (vanilla Rust doesn't load plugins); the section puts plugins in the folder that framework reads (`oxide/plugins` or `carbon/plugins`).

- uMod only offers each plugin's newest version, so that's what's installed, checked against the hash uMod lists.
- **Installed** names plugins by their file (`Kits.cs` is Kits) and offers an update when yours isn't uMod's newest. Plugins you changed yourself look out of date too.
- Plugins' settings are in `oxide/config` (or `carbon/configs`) in the file manager.
- Codefling and other paid plugin sites aren't offered: their downloads need a login. Upload those in the file manager.

## Bedrock players (Geyser)

Java servers running Paper (and Purpur, Folia, Spigot), Velocity, Waterfall, BungeeCord, Fabric, or NeoForge can let **Bedrock** players join: phones, consoles, and Windows. On **Network**, under **Bedrock players**:

1. **Let Bedrock players join** installs [Geyser](https://geysermc.org) and, unless you untick it, Floodgate (so Bedrock players don't need a Java account). Each is checked against its source's hash like any add-on. On Velocity, Waterfall, and BungeeCord, Floodgate is left out: on a proxy it also needs its key on every server behind it.
2. **Restart the server:** Geyser makes its settings on its first start.
3. **Finish setting up** puts Bedrock on the server's own port, so Bedrock players use the same address and port as Java players. (Raptor publishes every port for both TCP and UDP; Bedrock uses UDP.)

The card then shows the address and port to give Bedrock players. If they can't connect, check your provider's firewall lets **UDP** through on that port. Setting it up needs the **Change files** permission.

## Bedrock packs

Bedrock servers (Mojang's server, and Endstone) get a **Packs** section in Add-ons for behaviour and resource packs.

- **Add a pack** takes a `.mcpack` or a `.mcaddon` (which holds several). Raptor reads each pack's manifest, puts it in `behavior_packs/` or `resource_packs/`, and turns it on for the loaded world. Restart the server to load it.
- Start the server once first: the world has to exist before packs can be turned on for it.
- Packs already in the folders show up too: turn them on or off for the world, or remove them (the folder goes to the trash).
- A pack marked **Needs Beta APIs** uses scripts; turn on the world's Beta APIs experiment (in the world's settings in the game, then upload the world) or it won't run.

Raptor puts resource packs on the world's list, but players only download them if `texturepack-required=true` is set in `server.properties`.

## Steam Workshop

Games that load Steam Workshop items themselves get a **Workshop** section in Add-ons: Garry's Mod (a collection), Counter-Strike 2 (a map collection), ARK (mods), Conan Exiles (mods, loaded in the order you list them), and Squad (items). Project Zomboid, Don't Starve Together, and Unturned get one too ([below](#games-that-list-mods-in-a-file)).

- Paste a Workshop link or ID: Raptor shows what it is, and refuses items for another game.
- For games that take a list, a collection's link adds its items.
- The page says whether a **restart** or a **reinstall** picks the change up.

The game downloads the items itself; Raptor only changes the setting that lists them. It needs the **Startup settings** permission.

### Games that list mods in a file

Project Zomboid, Don't Starve Together, and Unturned read their Workshop items from a config file, so their Workshop section edits that file, the same way Files does. Seeing the items needs **See files**, and changing them needs **Change files**. Only the list changes: the rest of the file, comments included, stays as it is. The game downloads new items the next time it starts.

- **Project Zomboid** (`.cache/Server/<server name>.ini`): two lists. **Workshop items** are what the game downloads, and **Mods to load** are the mod IDs inside them, which aren't the same numbers. Once the game has downloaded an item, its mod IDs show as buttons to add, so a new item takes one restart before its mods can be picked. You can also type a mod ID from the item's Steam page. Start the server once first, because the game makes the file on its first start.
- **Don't Starve Together**: the items go in `mods/dedicated_server_mods_setup.lua` (which downloads them) and in each shard's `modoverrides.lua` (which turns them on). A mod's settings in `modoverrides.lua` are kept, and removing an item removes its entry. Leave **Validate** off: a validated update puts back the game's own setup file.
- **Unturned** (`Servers/<server ID>/WorkshopDownloadConfig.json`, `File_IDs`): start the server once first, because the game makes the file on its first start.
## Counter-Strike 2 plugins

Counter-Strike 2 servers get a **Frameworks** section in Add-ons, for the two frameworks CS2 server plugins run on:

- **Metamod:Source**, the plugin loader every CS2 server plugin needs.
- **CounterStrikeSharp**, which runs C# plugins. It runs on Metamod, so turning it on loads Metamod too.

Turn one on and choose **Save and reinstall**: Raptor backs up the server's files, reinstalls it, and downloads the framework into `game/csgo/addons`. Your maps, configs, and plugins stay. Each has a **Version**: `latest` (the newest, updated at every reinstall), or a Metamod build number (`1491`) or CounterStrikeSharp release (`1.0.380`) to stay on.

Raptor adds Metamod to `gameinfo.gi` every time the server starts, so a game update (which replaces that file) doesn't turn it off. When a CS2 update breaks the frameworks, reinstall once their authors have released a fix to get the newest builds.

Turning one off takes effect at the next start, and nothing is deleted. Raptor only undoes what it did, so Metamod or CounterStrikeSharp you installed by hand is left alone.

Then put CounterStrikeSharp plugins in `game/csgo/addons/counterstrikesharp/plugins` with the file manager, and restart. The page needs the **Startup settings** permission, and **Reinstall** to install.

The same switches are in **Startup** as **Metamod:Source** and **CounterStrikeSharp**. Servers made before they existed get them once they move to the egg's new version (see [Egg library](/eggs/library/)).
## Arma Reforger mods

Arma Reforger servers get a **Mods** section in Add-ons for mods from the [Arma Reforger Workshop](https://reforger.armaplatform.com/workshop).

- **Search** the Workshop, or paste a mod's link or ID (the 16 letters and digits in its link). Adding a mod adds the mods it needs too.
- Each mod shows its newest version. **Pin** one to keep it on the version it's on; unpinned mods always get the newest.
- If the Workshop doesn't know an ID (an unlisted mod), type its name and add it anyway.
- **Save** writes the list to the server's `config.json` (its `game.mods`); everything else in the file stays as it is. Restart the server: it downloads the mods itself when it starts.

It needs the **Files** permissions, since it edits a file. To change the file by hand, use **Edit config.json as text**.
