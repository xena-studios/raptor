---
title: Server settings
description: Limits, crash and stop behaviour, startup and game settings, Minecraft's Game tab, ports, Minehut, changing the game, reinstalling, moving, and deleting.
sidebar:
  order: 2
---

A server's settings are spread over a few tabs: **Settings** for its name, limits, and behaviour; **Startup** for its runtime and game settings; **Network** for its ports and address; and, for Minecraft Java servers and proxies, **Game** for the game's own settings and version.

Most changes take effect the **next time the server starts**; the app says when one needs a restart. Changes that alter what code runs (the runtime, the startup command, the game) are signed with your passkey.

## Name and limits

On **Settings**:

| Setting | What it does |
|---|---|
| **Name** | What it's called in Raptor. Up to 64 characters. |
| **Memory** | The most memory the game may use. Too little and it crashes with "out of memory"; Minecraft wants 2–4 GB for a few friends, more with mods. |
| **Disk limit** | The most space its files may take, including its trash. Empty means no limit. Applies at once. |
| **Swap** | Disk the game may use as extra memory once its memory is full. Much slower than memory; most games run better without it. |
| **CPU limit** | The most CPU it may use, in cores (0.5 is half a core, 2 is two). Without one, every server shares the machine's CPU fairly, which is usually best. |

## Crashes and stopping

| Setting | What it does |
|---|---|
| **Restart it when it crashes** | On by default. Raptor starts it again after a crash, and gives up after three in ten minutes ([how](/servers/overview/#crashes)). Off, a crashed server stays down until someone starts it. |
| **A clean exit means it stopped** | Off by default. When the game quits without an error, as after `/stop` in its own console, it's left stopped instead of counted as a crash and restarted. |
| **Force stop after** | How long **Stop** waits for the game to save and shut down before it's killed: 1 to 600 seconds, 60 by default. Big worlds can need longer. |

These apply at once, even while the server runs.

## Startup and game settings

On **Startup**:

- **Startup command:** what the server runs, built from its game's recipe (its [egg](/eggs/library/)).
- **Runtime:** for games with more than one, such as Java versions for Minecraft. Changing it is signed with your passkey.
- **Game settings:** versions, jar names, world names, passwords, and the like, each with what it does. Some are fixed by the game's recipe and shown greyed out; members only see the ones they're allowed to.
- **Modpack** (Modrinth, FTB, and Technic modpack servers): **Choose another modpack** picks a pack and version (Modrinth and FTB list the most installed first; for Technic, type the pack's name) and fills in its settings. Save, then [reinstall](#reinstall) to install it. Back up first: the pack's mods and configs are replaced.

Games with plugins, mods, datapacks, packs, or Steam Workshop support get an **Add-ons** tab, with a section for each: see [Add-ons](/servers/addons/).

## The Game tab

Minecraft Java servers, and Velocity, Waterfall, and BungeeCord proxies, get a **Game** tab, right after Overview, for anyone with the **See files** permission. Servers get **Version**, **In the server list**, and **Game settings**; proxies get **Version** (Velocity and Waterfall) and **Backends**.

- **Version:** the Minecraft (or proxy) version it runs, picked from the software's own list: Paper, Purpur, Folia, Velocity, Waterfall, vanilla, Fabric, or Quilt (other software, such as forks like Leaf, keeps its version setting in Startup). **The newest** keeps it on the newest release at every reinstall; **Show snapshots and pre-releases** adds those to the list. **Change to …** backs up the server's files, then reinstalls it with that version; the world, plugins or mods, and settings stay. Stop the server first. Going back to an older version warns you: a world opened in a newer version may not load in an older one, and the backup is how you'd undo it. A newer version may need a newer Java: if so, Overview offers to switch it. It needs the **Startup settings** permission, and **Reinstall** to install straight away (without it, the version is saved for someone who can reinstall).
- **Backends** (Velocity): the servers behind the proxy, tried in the order shown when players join. **Add one of your Paper servers** (Paper, Purpur, Folia, and forks) and **Save**: Raptor writes it into `velocity.toml`, turns on Velocity's modern forwarding, and sets the backend to take players only through this proxy (its `config/paper-global.yml` gets Velocity's secret, and `online-mode` goes off, since the proxy checks accounts). Nobody can then skip the proxy and join the backend as someone else. Restart the proxy and the backends afterwards. Start each once first, so their settings files exist. Removing a backend only takes it out of Velocity. Needs the **Change files** permission on the proxy and the backend.
- **Backends** (Waterfall and BungeeCord): the same, in the proxy's `config.yml`, for your Spigot and Paper servers. Raptor turns on BungeeCord forwarding on the proxy (`ip_forward`) and the backend (`bungeecord` in `spigot.yml`), and turns the backend's `online-mode` off. **BungeeCord forwarding has no secret:** anyone who can reach a backend's port directly can join it as any player, admins included. Only add servers that the internet can't reach (put them on a [private network](/nodes/networks/) with the proxy), or use Velocity, whose forwarding is signed.
- **In the server list:** the icon and message (MOTD) players see before they join, with a preview. Upload any image for the icon (it's cropped square and made 64×64, saved as `server-icon.png`), and use the colour and style buttons for the message instead of typing § codes. It shows once the server has started once and made its `server.properties`. Restart the server to show changes.
- **Game settings:** Minecraft's own settings (`server.properties`) as a form: difficulty, game mode, PvP, the whitelist, max players, view distance, the world's seed and type, and the rest, each saying what it does. The form shows the settings your Minecraft version has, so it follows the version you run. **↺** puts one back to its default. The port is set on Network; settings the form doesn't know are listed and left alone, and **Edit as text** opens the file. Restart the server to use changes.

Saving backends, the server list look, and game settings needs the **Change files** permission.

## Ports and address

On **Network**:

- **Port:** the port players connect to, 1024–65535, not used by another server on the node. It changes at the next start.
- **Shown address:** what players should connect to, if not the node's own address: a domain (`play.example.com`), a domain with an SRV record, or a proxy like TCPShield. It's shown exactly as you type it, so add the port if players need one. **Use your own domain** shows the DNS records to add for a name you own, ready to copy: see [Use your own domain](/help/your-own-domain/).
- **Networks** (admins and owners, on nodes with [networks](/nodes/networks/)): which network the server is on, and others it also joins, like a proxy. It changes at the next start.
- **Bedrock players** (most Minecraft Java servers and proxies): lets Bedrock players join with Geyser. See [Bedrock players](/servers/addons/#bedrock-players-geyser).
- **Connection test:** checks from outside whether players can reach the server, and says what to change if they can't. See [Players can't connect](/help/players-cant-connect/).

## Minehut

Minecraft servers running Velocity, Waterfall, or Paper (and Purpur or Folia) can join [Minehut](https://minehut.com)'s network as an **external server**, so players find them in Minehut's lobby and connect through its DDoS protection. On **Network**, **Set up for Minehut** makes the changes [Minehut's guide](https://support.minehut.com/hc/en-us/articles/33252734474899-How-to-Setup-a-Minehut-External-Hosted-Server) asks for:

- **Velocity:** turns on the HAProxy protocol in `velocity.toml`, and adds Minehut's login flag to the startup command.
- **Waterfall:** turns on the proxy protocol in `config.yml`, and adds the login flag.
- **Paper:** turns on the proxy protocol in `config/paper-global.yml`, turns off `enforce-secure-profile`, and adds Minehut's login flags.

It needs the **Startup settings** and **Change files** permissions, and your passkey signs the startup change. Start the server once before, so its config files exist, and restart it after. If you use TCPShield, tick it, and the proxy protocol changes are left out. Then the card shows what to type into Minehut's **External Server Settings** (the IP is the node's address, which follows its IP), with copy buttons. **Undo Minehut's changes** puts it all back.

After setting up, players can join **only through Minehut**, not the server's own address, and the player count may stop showing. BungeeCord isn't supported by Minehut: switch to Waterfall or Velocity. Minehut takes new external servers from its Partners Program and charges credits for them, so check your Minehut account first.

## Change the server's type

**Server type** on **Settings** (admins and owners) changes what the server runs: Paper to Purpur, Forge to NeoForge, or another game entirely. Pick the new type, then what happens to the files:

- **Keep them and reinstall:** after a backup you can restore from, the new install runs over the files the server has, so worlds and configs stay. Best between versions of the same game.
- **Start fresh:** every file is deleted, after a backup you can restore from, and the new type installs clean.

Settings both types share (a Minecraft version, say) keep their values; the rest start at the new type's defaults. The server must be stopped, and your passkey signs the change.

## Egg version

A server keeps its own copy of the [egg](/eggs/library/) it was made with, so editing the egg in your library never changes a server by itself. **Settings** shows **Egg version** (admins and owners): which version of the egg the server runs, and whether it's the newest (**Update available** when it isn't). A copy that isn't one of the library's versions, because it was edited or made before the library kept versions, says so.

Pick a version, newer or older, and **Update to version N** (or **Go back to version N**). It lists what changes: runtimes added or removed, a new startup command, new or removed settings, and whether the install script changed. Your passkey signs it.

- The server's runtime stays, unless the new version dropped it.
- A startup command you changed stays; one you didn't becomes the new version's.
- Settings keep their values. A new required setting with no default asks for one.
- **Reinstall now** is off by default: most changes apply the next time the server starts. Turn it on (with the server stopped) when the install script changed and you want it run again. It runs over the files, after a backup you can restore from.

Updating the egg doesn't update the game. For a newer Minecraft version, say, change the version setting and reinstall.

## Reinstall

**Reinstall** on **Settings** runs the install script again, for a broken install or to pick up a change to its game settings.

- **Reinstall:** a backup is taken, then the install runs over what's there. Usually what you want.
- **Wipe and reinstall:** a backup is taken, every file is deleted, then it installs clean. Signed with your passkey.

Either way, if the backup fails, nothing is changed and the console says why. A server with no files yet needs no backup. These backups are listed as safety backups on **Backups** and kept for 7 days.

The server must be stopped, and you type its name to confirm.

When its egg has a newer version, **Update the egg to version N too** does both at once: you see what changes, as for [Egg version](#egg-version), and it installs with the new version. It's off unless you tick it.

## Details

**Details** on **Settings** shows the server's ID, for support and the [raptor command](/cli/reference/), with its node and game.

## Move to another disk

When the node has [more than one disk](/nodes/disks/) for servers, the **Disk** card on **Settings** (admins) moves the server's files to another. It stops the server, copies everything, switches over, and only then removes the old copy, so nothing is lost if it's interrupted. It starts again afterwards if it was running.

## Delete

**Delete server** (admins and owners) is permanent. Type its name and choose **Delete with passkey**.

- Its files, local backups, schedules, and ports go with it. If you might want it back, [back it up](/servers/backups/) offsite first.
- A deleted server's offsite backups stay in the Panel, to restore onto another server with your passkey.
