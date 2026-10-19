---
title: The server page
description: The console, live stats, power actions, and what each state means.
sidebar:
  order: 1
---

Each server opens on its **Overview**: what it's using now, the live console, how players reach it, and its last hour.

## Stats

The tiles at the top update every 10 seconds:

- **CPU:** 100% is one whole core. A server with a CPU limit shows how close it is to it.
- **Memory:** what the game uses, out of its memory limit.
- **Disk:** what its files take, out of its disk limit (or "No limit").
- **Network:** traffic in and out, now.
- **Players:** for games that answer a status query (Minecraft Java, and Steam games that answer A2S). A game that doesn't says so.

Below the console, **Resources** charts CPU, memory, disk, network, and players over the last hour, 6 hours, day, or week. The machine keeps a minute-by-minute history for a day and 15-minute points for a week.

## The console

The console shows the server's output live, starting with its last 1,000 lines. Type a command in the box below it and press Enter: it goes to the game exactly as typed.

- Watching it needs the **See the console** permission, and typing in it **Send console commands**.
- Lines from Raptor itself start with `[raptor]`: starting, stopping, crashes, install results.
- Pop the console out into its own window with the button at its top right, or make it fill the page.
- For history further back than the console keeps, use `sudo raptor logs <server>` on the machine.

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

- A crash's alert includes the exit code, why (out of memory, killed, or exited), and the last 50 lines of the console.
- **Out of memory** means the game needed more than its memory limit: raise it in [Settings](/servers/settings/).
- A `/stop` typed in the game's own console counts as a crash by default (as in Pterodactyl). Turn on **A clean exit means it stopped** in [Settings](/servers/settings/#crashes-and-stopping) to change that.
- Turn auto-restart off there too, if you'd rather it stayed down.

## The address

**Details** shows the address players connect to: the node's address and the server's port, hidden until you reveal it (so it isn't on screen while you stream). Copy it without revealing it. If players connect through a domain or a proxy, set a **shown address** on the Network tab. See [Players can't connect](/help/players-cant-connect/) if they can't.
