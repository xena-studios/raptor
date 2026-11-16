---
title: Worlds
description: A Minecraft server's worlds and game rules, from the app.
sidebar:
  order: 6
---

Minecraft Java servers (not proxies) have a **Worlds** tab, after Players, for what you'd otherwise do in the file manager. Seeing it needs the **See files** permission.

## Your worlds

Raptor lists every folder with a `level.dat` (a world), at the top of the server's files or one folder in. The **Loaded** one is the world the server starts with (`level-name` in its settings). Paper's separate Nether and End folders (`world_nether`, `world_the_end`) go with their world.

- **Load this one:** the server loads that world from its next start. Restart it to switch now.
- **Zip to download:** makes a zip of the world (with its Nether and End) in the server's files; download it from **Files** when it's ready.
- **Upload a world:** a zip (or tar) of a world folder. It's unpacked into a folder named after the file; then load it.
- **Delete:** a world you're not loading goes to the server's [trash](/servers/files/#the-trash), where you can get it back for a while.

Changing worlds needs the **Change files** permission.

## Start a new world

With the server stopped, **Start a new world** moves the loaded world (and its Nether and End) to the trash, and the server makes a fresh one at its next start. Give a **seed** to choose the world, or leave it empty for a random one. Players' items and progress in the old world go with it, so make a [backup](/servers/backups/) first if you might want it back after the trash empties.

## Game rules

The loaded world's game rules, such as keepInventory, mob griefing, and how many players must sleep to skip the night, as of its last save. The common ones say what they do; **Show every rule** lists the rest. While the server runs, change one and Raptor tells the game (`gamerule`), which takes effect right away. It needs the **Send console commands** permission.
