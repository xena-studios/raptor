---
title: Deploy your first server
description: Pick a game, give it a name, and Raptor installs and starts it on your node.
sidebar:
  order: 2
---

Once your node is [connected](/install/first-node/), deploying a server takes a minute.

1. Open **Servers** and choose **Deploy server**.
2. **Node:** pick the node to run it on. It has to be online.
3. **Game:** pick what the server runs. Search the catalog, or pick one of your org's own games.
4. **Settings:** give it a name, and check the rest:
   - **Runtime:** what the server runs in, such as its Java version, when the game offers more than one. The first is the game's own choice.
   - **Port:** the port players connect to. The app starts at the game's usual port (25565 for Minecraft) and picks the next free one if another server has it.
   - **Memory:** how much the server may use. The app starts at 2 GB for Minecraft and 4 GB for Steam games, never more than three quarters of the node's.
   - **Disk limit:** how much space its files may take. Leave it empty for no limit.
   - **Game settings:** versions, jar names, and the like. They work as they are, so they're folded away. Anything the game can't start without is shown above them.
5. For Minecraft and other games with one, accept the game's EULA.
6. Choose **Create with a passkey**. Your passkey signs exactly this server, so the node knows it's you.

Raptor downloads and installs the game, then starts it. The console on the server's **Overview** shows it happening. When it's running, the server's address (the node's address and the port) is on the Overview too: give that to your players.

If players can't join, see [Players can't connect](/help/players-cant-connect/).

## What's on a server's page

- **Overview:** the console (the server's output, live, and a box to type commands), its address, and its power buttons and stats. [More](/servers/overview/)
- **Files:** a file manager in the browser, and temporary SFTP logins for big uploads. [More](/servers/files/)
- **Schedules:** restarts, backups, and commands on a timer. [More](/servers/schedules/)
- **Backups:** back up now, restore everything or just some files, and choose where backups go. [More](/servers/backups/)
- **Network:** the server's ports, and the connection test.
- **Startup:** the command it starts with, and its game settings.
- **Settings:** its name and limits, what happens when it crashes or stops, changing its game, reinstalling it, and deleting it. [More](/servers/settings/)
- **Activity:** who did what to it, and when. [More](/team/activity/)
- **Access:** what each member of your org may do on this server. [More](/team/members/#what-members-can-do-on-a-server)
