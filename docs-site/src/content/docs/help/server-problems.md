---
title: Server problems
description: A server that won't start, keeps crashing, failed to install, or runs out of memory or disk.
sidebar:
  order: 4
---

The console says most of what's wrong: open the server's **Overview** and read its last lines. Lines starting `[raptor]` are from Raptor itself.

## It crashed

A server that stops on its own is restarted, and after three crashes in ten minutes it's left **crashed** ([why](/servers/overview/#crashes)). The crash alert has its last 50 console lines.

- **"Out of memory"** (or `oom`): the game needed more than its memory limit. Raise **Memory** on [Settings](/servers/settings/#name-and-limits). Modded Minecraft often needs 6 GB or more.
- **A Java error about the class file version:** the game needs a newer Java. Pick a newer **Runtime** on Startup.
- **It stopped after someone typed `/stop`:** that counts as a crash by default. Turn on **A clean exit means it stopped** in Settings.
- **It crashes right after a plugin or mod change:** remove the new one (it goes to the trash), and start it again.

## It won't start

- **"install failed":** see below.
- **The port is in use:** another program on the machine has it. Pick another port on **Network**, or stop that program.
- **"over its disk limit":** raise the limit, or free space, including the trash.
- **The node is offline:** nothing can start until it's back. See the node's page.

## The install failed

The console shows the install script's output and where it stopped, and the server's status says why.

- **"A download failed: … Reinstall to try again."** The install couldn't download something even after retrying, usually because the site was down or the connection broke off. **Reinstall** on Settings when it's back. If it keeps failing, check the machine can reach the internet with `sudo raptor doctor`.
- **"server.jar isn't a valid jar file"** (or **"is empty"**): the download came back broken or as an error page. Reinstall to try again.
- **The version doesn't exist:** the version in Startup might be a typo, or too new. Check it, then **Reinstall** on Settings.
- **A network error:** the machine couldn't reach the internet. Check with `sudo raptor doctor`.
- **It ran out of disk:** raise the disk limit and reinstall.

## It's slow, or lags

- Check **CPU** and **Memory** on the Overview, and the charts below. A server at its CPU limit lags; raise or remove the limit.
- **Swap** makes a server slow once it's in use: give it more memory instead.
- Check the whole machine on the node's **Overview**: other servers may be using it.
- Lots of servers backing up at once can slow a machine. Spread their schedules out, or limit a destination's upload speed.

## Players can't join

See [Players can't connect](/help/players-cant-connect/).

## Still stuck

Run `sudo raptor doctor` on the machine, then [ask for help](/help/get-help/) with what the console says.
