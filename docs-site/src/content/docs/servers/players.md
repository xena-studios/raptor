---
title: Players
description: A Minecraft server's whitelist, operators, and bans, from the app.
---

Minecraft Java servers have a **Players** tab, next to Overview, for who may join and who runs the place: the whitelist, operators, banned players, and banned addresses, plus everyone who's joined before.

## Running or stopped

- **While the server runs**, a change is made by telling the game, exactly as typing `whitelist add`, `op`, `ban`, or `pardon` in the console would, and the lists update a moment later. **Kick** is there too. It needs the **Send console commands** or **Manage players** permission.
- **While it's stopped**, a change is saved to the game's files (`whitelist.json`, `ops.json`, `banned-players.json`, `banned-ips.json`), and the server uses them when it starts. Raptor looks the player's account up with Mojang to fill in their ID; on a server in offline mode it uses the ID offline mode gives the name. It needs the **Change files** or **Manage players** permission.

## Moderators

Give someone **Manage players** in the server's **Access** to let them whitelist, op, ban, unban, and kick without the console or the files. Raptor builds each command itself, so they can't run anything else. Members who could already send console commands have it.

## The lists

- **Whitelist:** only these players can join, when the whitelist is on. Turn it on (and **Enforce the whitelist**, to kick players taken off it) in **Game → Game settings**.
- **Operators:** they can run every command. New operators get level 4; change the level in `ops.json` if you want less.
- **Banned players** and **Banned addresses:** with a reason, if you give one.
- **Seen before:** everyone who's joined (`usercache.json`), with buttons to whitelist, make operator, kick (while running), or ban.

Names are letters, digits, and underscores, up to 16, as Minecraft's are; anything else is refused before it reaches the server.
