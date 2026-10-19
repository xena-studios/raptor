---
title: Schedules
description: Restarts, backups, and commands on a timer, in steps, in your time zone.
sidebar:
  order: 5
---

Schedules run on the machine's own clock, so they keep running even when Raptor's Panel can't be reached. Find them on a server's **Schedules** tab (the **Schedules** permission).

## Make one

Choose **New schedule**, then:

1. **When:** pick an hour and days, or write a cron expression (`0 4 * * *` is 4 AM every day; `*/30 * * * *` every half hour; `@daily`, `@weekly` work too). The app shows the next few times it will run.
2. **Time zone:** the schedule's own, so "4 AM" means 4 AM where your players are. It starts at the node's.
3. **Steps,** run in order (up to 20):

| Step | Does |
|---|---|
| **Command** | Sends a console command, like `say Restarting in 5 minutes` |
| **Wait** | Pauses, from a second to an hour |
| **Power** | Starts, stops, restarts, or kills the server |
| **Back up** | Backs the server up and waits for it, to every destination or just one |

A step that fails ends the run, unless it's set to **carry on if it fails**.

A typical nightly restart:

```
Command   say Restarting in 5 minutes
Wait      4 minutes
Command   say Restarting in 1 minute
Wait      1 minute
Back up
Power     restart
```

## Options

- **Turned on:** untick it to pause a schedule without losing it.
- **Skip runs while the server is offline:** on by default, since there's nothing to restart or back up. Turn it off for schedules that start the server.

**Run now** runs a schedule straight away, whatever its options.

## Runs

Under the schedules, **Runs** lists each run: running (with the steps done), finished, failed (at which step, and why), or skipped (and why). The last 100 of each schedule are kept.

## Good to know

- **Only one run at a time:** if the last one is still going, the next is skipped.
- **Missed runs:** a run up to 5 minutes late (the machine restarting) still runs; one missed while the machine was off is skipped, except the Daily backup, which runs once when it can. Missed runs are never replayed one after another.
- **The Daily backup is spread out:** it starts at a moment between 4 and 5 AM that's the same every day, so every server on a node doesn't back up at once.
- **Daylight saving time:** a time skipped when the clocks go forward runs once, when they change; a time that happens twice runs once.
- **If the machine restarts mid-run,** the run carries on after its last finished step. If the machine was down for more than 5 minutes, the rest of the run is skipped: restarting long after the players were warned would be worse.
- Up to 50 schedules per server.
