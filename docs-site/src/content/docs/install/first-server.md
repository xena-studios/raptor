---
title: Deploy your first server
description: Pick a game, give it a name, and Raptor installs and starts it on your node.
sidebar:
  order: 2
---

Once your node is [connected](/install/first-node/), deploying a server takes a minute.

Admins and owners deploy servers; members can use the ones they're given.

1. Open **Servers** and choose **Deploy server**.
2. **Node:** if you have more than one, pick the node to run it on. It has to be online.
3. **Game:** pick what the server runs from the [egg library](/eggs/library/), most popular first (your org's own eggs come before Raptor's), or search for it. Only what the node's processor can run is listed. For a game Raptor doesn't have yet, [import an egg](/eggs/library/#importing-one) on the Eggs page.
4. **Settings:** give it a name, and check the rest:
   - **Runtime:** what the server runs in, such as its Java version, when the game offers more than one. The first is the game's own choice.
   - **Port:** the port players connect to. The app starts at the game's usual port (25565 for Minecraft) and picks the next free one if another server has it.
   - **Memory:** how much the server may use. The app starts at 2 GB for Minecraft and 4 GB for Steam games, never more than three quarters of the node's.
   - **Disk limit:** how much space its files may take. Leave it empty for no limit.
   - **Network** (nodes with [networks](/nodes/networks/)): which network it joins. It reaches the servers on that network, and nothing on the others.
   - **Game settings:** versions, jar names, and the like. They work as they are, so they're folded away. Anything the game can't start without is shown above them.
   - **Modpack** (Modrinth, FTB, and Technic modpack servers): **Browse modpacks** searches that site's packs; pick one and a version, and the pack's settings are filled in for you.
5. For Minecraft and other games with one, accept the game's EULA.
6. Choose **Create with a passkey**. Your passkey signs exactly this server, so the node knows it's you.

Raptor downloads and installs the game, then starts it. The app takes you to the node's page, where the new server is listed: open it, and the console on its **Overview** shows the install happening. When it's running, the server's address (the node's address and the port) is on the Overview too: give that to your players.

If players can't join, see [Players can't connect](/help/players-cant-connect/).

## What's on a server's page

- **Overview:** the console (the server's output, live, and a box to type commands), its address, its power buttons and stats, and **Needs attention** when something needs you. [More](/servers/overview/)
- **Game, Players, and Worlds** (Minecraft Java; proxies get Game): Minecraft's settings as a form, its version, the whitelist, operators, and bans, and its worlds and game rules. [Game](/servers/settings/#startup-and-game-settings), [Players](/servers/players/), [Worlds](/servers/worlds/)
- **Files:** a file manager in the browser, and temporary SFTP logins for big uploads. [More](/servers/files/)
- **Add-ons** (games that have them): plugins, mods, datapacks, and Steam Workshop items. [More](/servers/addons/)
- **Schedules:** restarts, backups, and commands on a timer. [More](/servers/schedules/)
- **Backups:** back up now, restore everything or just some files, and choose where backups go. [More](/servers/backups/)
- **Databases:** the server's databases and their details for plugins. [More](/servers/databases/)
- **Network:** the server's ports, its shown address, and the connection test. [Players can't connect](/help/players-cant-connect/), [Use your own domain](/help/your-own-domain/)
- **Startup:** the command it starts with, and its game settings.
- **Settings:** its name and limits, what happens when it crashes or stops, changing its game, reinstalling it, and deleting it. [More](/servers/settings/)
- **Activity:** who did what to it, and when. [More](/team/activity/)
- **Access:** what each member of your org may do on this server. [More](/team/members/#what-members-can-do-on-a-server)
