---
title: Try the demo
description: Try a real Raptor Panel for an hour, with no account, no passkey, and no machine of your own, and make a server of your own from any game that fits.
sidebar:
  order: 6
---

The demo is a real Raptor Panel with real game servers on a real machine: the console streams, the graphs move, and add-on search works. Open it from **Try the demo** on [raptorpanel.net](https://raptorpanel.net), or go to [app.raptorpanel.net/demo](https://app.raptorpanel.net/demo). There's a quick security check, then **Start the demo**. You don't need an email or a passkey.

If the page says "The demo isn't open right now", it's being set up or looked after. Try again later.

## Make a server of your own

The point of the demo is to try games. Open **New server**, pick a game from Raptor's egg library, and you get a server of your own, a **sandbox**:

- It installs and starts by itself. The bar at the top of every page shows how it's going, then a countdown.
- It's yours for **15 minutes** from when it's ready. If nobody is waiting, **+15 min** gives you 15 more, once.
- The bar shows its **address**: join it from your game (Minecraft, Terraria, Factorio, and the rest).
- Open its console, files, add-ons, and settings like any server. You can be an operator on your own sandbox.
- One at a time, per visitor and per address. **Delete** ends it early, so you can try another game.

The demo runs four sandboxes at once. When they're all taken, you're in a queue, and the bar says your place and about how long until yours starts. The most popular games start in seconds; others install first, which can take a few minutes.

**Which games:** most of the library fits a sandbox (1 CPU core, 1.5 GB of memory, 10 GB of disk): most Minecraft software, the standalone games (Terraria, Factorio, Luanti, Veloren, …), the generic runtimes (Node.js, Python, Go, …), and the smaller Steam games. Games with big downloads or that need more memory (ARK, DayZ-likes, anything over a few GB) are listed but marked **Too big for the demo**. Those run on your own node: [connect your first node](/install/first-node/).

Sandboxes reach only what games need on the internet (Mojang, Modrinth, Hangar, PaperMC, uMod, Steam, CurseForge, Thunderstore), so a few plugins that call other services won't work in the demo. They work in your own Raptor.

## Try your own egg

**Eggs → New egg** in the demo lets you import an egg file (or start from scratch), see what it would run, and edit it with Raptor's egg editor. It can't be saved or run in the demo: an install script from an anonymous visitor would be code running on our machine. The library's eggs are reviewed. In your own Raptor, save it and make servers from it.

## The shared servers

You also get the demo's showcase servers, shared with every visitor: a Paper server with plugins, a Fabric server with mods, and Uptime Kuma. You can:

- Watch the console and send commands, and start and stop servers.
- Browse and edit files, and search and install add-ons.
- Make and run schedules.
- Take backups and look inside them.
- Run the connection test, and see the activity.
- Change settings like memory.

On the shared servers, console commands that give anyone operator or permissions (`op`, `whitelist`, `execute`, permission plugins) and broadcasts to their players (`say`, `tellraw`) are off.

## What's off

Other visitors share the same machine, so the demo turns off what could break it for everyone or reach outside Raptor. Trying one of these says **"That's off in the demo: in your own Raptor, you can do it."**

- Anything your passkey signs in your own Raptor, other than your sandbox: deleting the shared servers, wipe and reinstall, changing a server's image, and restoring backups. Those buttons say **"In your own Raptor, your passkey signs this."**
- SFTP, **Upload → From a link**, and importing from another host. Uploads are up to 10 MB.
- External database access, networks, the firewall, and node settings.
- Billing, members and invitations, support access, sharing logs to mclo.gs, and deleting the org.
- Changing your account: there's no email or passkey to change.

## Resets

A banner on every page says **"You're in the demo. It resets every hour."** Every hour, guests are signed out, the shared servers are put back the way they were, and their trash is emptied. Your hour starts when you open the demo, so you might be signed out sooner. Start again from the demo page to get another hour. Sandboxes end on their own time, and the whole demo machine is rebuilt from scratch every month.

Don't put anything private in the demo: other visitors can see the shared servers' files and consoles.

The demo counts visits and which pages they reach, to learn what helps people decide. It records no names, addresses, or anything else about you.

When you're ready for your own, [connect your first node](/install/first-node/).
