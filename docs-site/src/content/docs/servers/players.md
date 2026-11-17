---
title: Players
description: Who's on your server, and a Minecraft server's whitelist, operators, and bans, from the app.
sidebar:
  order: 5
---

Minecraft Java servers (not proxies) have a **Players** tab, after Game, for who's on now, who may join, and who runs the place: the whitelist, operators, banned players, and banned addresses, plus everyone who's joined before. It shows for anyone with **Manage players**, **See files**, or **Send console commands**. Many Steam games have one too, over RCON: see [Steam games](#steam-games) below.

## Running or stopped

- **While the server runs**, a change is made by telling the game, exactly as typing `whitelist add`, `op`, `ban`, or `pardon` in the console would, and the lists update a moment later. **Kick** is there too. It needs the **Send console commands** or **Manage players** permission.
- **While it's stopped**, a change is saved to the game's files (`whitelist.json`, `ops.json`, `banned-players.json`, `banned-ips.json`), and the server uses them when it starts. Raptor looks the player's account up with Mojang to fill in their ID; on a server in offline mode it uses the ID offline mode gives the name. It needs the **Change files** or **Manage players** permission.

## Moderators

Give someone **Manage players** in the server's **Access** to let them whitelist, op, ban, unban, and kick without the console or the files. Raptor builds each command itself, so they can't run anything else. Members who could already send console commands have it.

## The lists

- **Online now** (while the server runs): how many are on, and their names, with buttons to make operator, kick, or ban. Minecraft's own server list shows at most 12 names, and some servers hide them, so a busy server may show fewer names than its count; `list` in the console names everyone. It needs **Manage players**.

- **Whitelist:** only these players can join, when the whitelist is on. Turn it on (and **Enforce the whitelist**, to kick players taken off it) in **Game → Game settings**.
- **Operators:** they can run every command. New operators get level 4; change the level in `ops.json` if you want less.
- **Banned players** and **Banned addresses:** with a reason, if you give one.
- **Seen before:** everyone who's joined (`usercache.json`), with buttons to whitelist, make operator, kick (while running), or ban.

Names are letters, digits, and underscores, up to 16, as Minecraft's are; anything else is refused before it reaches the server.

## Steam games

Source games (Counter-Strike 2 and Source, Team Fortress 2, Garry's Mod, Left 4 Dead 1 and 2, and the other Source mods in the library), ARK, ARK: Survival Ascended, Palworld, and Rust get a **Players** tab that talks to the game over RCON, its remote console:

- **Online:** who's on now, with their ID, ping, and how long they've been on. It refreshes every 15 seconds.
- **Kick** and **Ban** on each player. A ban lasts a restart; the confirmation shows the player's ID, which is what you unban them by.
- **Unban:** paste the player's ID (a Steam ID, like `STEAM_1:0:123` in Source games or `76561198…` in Rust).
- **Message everyone:** one line in the game's chat.

In Source games RCON is off until it has a password: set **RCON password** in the server's **Startup** and restart the server (Counter-Strike 2 also needs **RCON** turned on there). ARK and Palworld use their **Admin password**, and Rust its **RCON password**, which they always have. The password stays on your node: Raptor's node software connects to the game itself, and the app never sees it. The tab works while the server runs; when it's stopped there's nobody to manage.

It needs the **Manage players** permission. Raptor builds every command itself and checks the ID and the text first, so a moderator can't run anything else on the server.
