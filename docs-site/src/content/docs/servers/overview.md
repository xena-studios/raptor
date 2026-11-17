---
title: The server page
description: The tabs, the console, live stats, power actions, and what each state means.
sidebar:
  order: 1
---

Each server opens on its **Overview**: what it's using now, the live console, how players reach it, and its last hour.

## Tabs

The row of tabs keeps to what you use most: **Overview**, then the game's own tabs when it has them (**Game**, **Add-ons**, **Players**, **Worlds**), then **Files**, **Backups**, and **Schedules**. The rest are under **More**, at the end of the row: **Databases**, **Network**, **Startup**, **Settings**, **Activity**, and **Access**. When you're on one of those, the menu shows its name. You only see the tabs you're allowed to use. On a phone, swipe the row sideways. To jump straight to a tab or an action, press **Ctrl+K** (**⌘K** on a Mac) and type: see [the command menu](/servers/command-menu/).

**Game** and **Startup** change the same values: Game as a friendly form, Startup as the exact list of variables. **Settings** is Raptor's settings for the server (its name, limits, and crash behaviour), not the game's.

## Stats

The tiles at the top update every 10 seconds:

- **CPU:** 100% is one whole core. A server with a CPU limit shows how close it is to it.
- **Memory:** what the game uses, out of its memory limit.
- **Disk:** what its files take, out of its disk limit (or "No limit").
- **Network:** traffic in and out, now.
- **Players:** for games that answer a status query (Minecraft Java, and Steam games that answer A2S). A game that doesn't says so.

Below the console, **Resources** charts CPU, memory, disk, network, and players over the last hour, 6 hours, day, or week. The machine keeps a minute-by-minute history for a day and 15-minute points for a week.

Servers with a web page of their own (FiveM's txAdmin when it's on, Grafana, code-server, Uptime Kuma, and other apps) get an **Open …** button under Details. If its port isn't one of the server's ports yet, it says to add it on **Network** first. The first time txAdmin starts, it prints a PIN in the console to create its admin account with; it's shown under **Open txAdmin** too.

## Resource scan

FiveM servers get a **Resource scan** card. Resources that were leaked or bought from strangers sometimes hide a backdoor: code that downloads and runs someone else's code, or gives them the console. **Scan resources** checks the Lua and JavaScript files in `resources` and `txData` for the patterns these backdoors use. The scan runs on the machine, so your files never leave it, and it changes nothing.

- Each file it flags is listed with the lines and how sure the scan is. **High** means honest resources almost never do this (code fetched from the internet and run, a known backdoor panel's address, a shell command, names hidden in escape codes). **Medium** is unusual and worth a look (obfuscated code, granting admin rights from code). **Low** is common in honest code too (running a config file as code).
- **Open** shows the file in the file manager. **Move to trash** (with the **Change files** permission) moves it to the [trash](/servers/files/#the-trash) after you confirm, so you can restore it. Restart the server afterwards. Raptor never deletes anything on its own.
- A flagged line isn't proof. Look at it, and if a resource you didn't write talks to an address you don't know, remove the whole resource and get it from its author.
- `node_modules` folders and escrowed (encrypted) resources aren't read. Very large servers may hit the scan's limits; it says so.
- Scanning needs the **See files** permission.

## Needs attention

When something about the server needs you, a **Needs attention** list sits at the top of its Overview, each line with the button that fixes it:

- **A problem in the console:** the wrong Java, running out of memory, a port in use, and the others in [Server problems](/help/server-problems/). When a server stopped because it needs another Java, the line says which (**This server needs Java 21**, say) and, if its egg has that Java, offers **Switch to Java 21 and start**. Your passkey signs it, and it needs the **Startup settings** permission (and **Start and stop** to start it).
- **The install failed:** the server can't start until it's reinstalled. Fix the cause the console shows, then **Reinstall**.
- **The disk is nearly full:** 90% of its limit or more. Delete old files or backups in **Files**, or ask for more space; a full disk can't save the world.
- **An add-on missing what it needs:** a mod or plugin that needs another one that isn't installed.
- **Add-on updates:** how many plugins, mods, or datapacks have a newer version.
- **A newer version of its egg** (admins and owners).
- **A possible backdoor:** what the last resource scan found, if anything high or medium (FiveM).

The worst comes first: what stops the server starting, then what stops players joining, then updates. If you can't do the fix yourself, the line says which permission can, so you know who to ask.

Dismiss a line with its ×: it stays hidden in this browser until what it says changes. With nothing to say, the list isn't there.

## The console

The console shows the server's output live, starting with its last 1,000 lines. Type a command in the box below it and press Enter: it goes to the game exactly as typed.

- Watching it needs the **See the console** permission, and typing in it **Send console commands**.
- Lines from Raptor itself start with `[raptor]`: starting, stopping, crashes, install results.
- Pop the console out into its own window with the button at its top right, or make it fill the page.
- For history further back than the console keeps, use `sudo raptor logs <server>` on the machine.
- **Share** (the button beside full screen) puts what the console shows on [mclo.gs](https://mclo.gs), a site for sharing game server logs, and gives you a link to send to whoever's helping you. IP addresses and the node's address are hidden first; anyone with the link can read the rest. Log files (`logs/latest.log`, crash reports) have the same button in the file manager. Each share is in the server's activity.

## Power

| Button | What it does |
|---|---|
| **Start** | Starts the server with its current settings. Changes to memory, ports, the runtime, and game settings take effect here. |
| **Stop** | Sends the game's own stop command, so it saves. If it hasn't stopped after the [stop timeout](/servers/settings/#crashes-and-stopping) (60 seconds unless you change it), it's killed. |
| **Restart** | Stop, then start. |
| **Kill** | Stops it at once, without saving. Unsaved progress is lost, so the app asks first. |

## States

| State | Meaning |
|---|---|
| **Installing** | Its install script is running. The console shows its output. |
| **Install failed** | The install didn't finish. The console says why; fix it and reinstall from Settings. |
| **Starting** | Started, and waiting for the game to say it's ready. |
| **Running** | Ready for players. |
| **Stopping** | Saving and shutting down. |
| **Offline** | Stopped. |
| **Crashed** | It stopped on its own, three times in ten minutes, so Raptor stopped restarting it. See below. |

When the node is offline, the server's state is the last one reported, shown faded.

## Crashes

Raptor restarts a server that stops on its own: at once after the first crash, 10 seconds after the second. A third crash within 10 minutes means something's wrong, so it leaves the server **crashed** and alerts you, instead of restarting forever. The crash count resets once it's been running for 10 minutes.

- A crash's alert includes the exit code, why (out of memory, killed, or exited), and the last 10 lines of the console.
- **Out of memory** means the game needed more than its memory limit: raise it in [Settings](/servers/settings/).
- A `/stop` typed in the game's own console counts as a crash by default (as in Pterodactyl). Turn on **A clean exit means it stopped** in [Settings](/servers/settings/#crashes-and-stopping) to change that.
- Turn auto-restart off there too, if you'd rather it stayed down.

## The address

**Details** shows the address players connect to: the node's address and the server's port, hidden until you reveal it (so it isn't on screen while you stream). Copy it without revealing it. If players connect through a domain or a proxy, set a **shown address** on the Network tab. See [Players can't connect](/help/players-cant-connect/) if they can't.
