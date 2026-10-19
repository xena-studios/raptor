---
title: How Raptor works
description: The two halves of Raptor, what runs where, and what happens when either one is offline.
sidebar:
  order: 1
---

Raptor has two halves: **the Panel**, the website you manage everything from, and **Wings**, the program that runs on your own machine and does the actual work.

```
   You (browser)             app.raptorpanel.net              Your machine (a node)
        │                          │                                 │
        │  click "Start"           │                                 │
        │─────────────────────────►│   a signed command              │
        │                          │────────────────────────────────►│  Wings starts the
        │                          │                                 │  server in Docker
        │  live console, stats ◄───│◄────────────────────────────────│
        │                          │                                 │
                                         Players connect straight to your machine ◄──
```

## The Panel

The Panel is hosted by Raptor at [app.raptorpanel.net](https://app.raptorpanel.net). It's where you:

- create and manage servers: console, files, backups, schedules, settings;
- invite your team and decide what each person may do;
- get alerts when something goes wrong;
- pay for your plan.

There's nothing to install or keep updated: Raptor runs it.

## Wings

Wings runs on every machine you link, which Raptor calls a **node**. It:

- runs each game server in its own Docker container, with the memory, CPU, and disk limits you set;
- restarts servers that crash, runs your schedules, and takes your backups;
- serves your files to the file manager and over SFTP;
- keeps its own records of everything, in a database on the machine.

Wings connects **out** to the Panel over an encrypted connection. You don't open a port for Raptor, and the Panel never logs in to your machine.

## Who does what

| | The Panel | Wings, on your machine |
|---|---|---|
| Your server files | Never stores them | Keeps them, and serves them |
| Running servers | Asks for changes | Runs them, restarts crashes |
| Schedules and backups | Shows and edits them | Runs them, on its own clock |
| Alerts | Sends them to you | Reports what happened |
| Dangerous actions | Passes your signed request on | Checks your passkey's signature itself |

## If Raptor goes offline

Your servers keep running, and players don't notice. Wings carries on with schedules, backups, and crash restarts on its own, and catches the Panel up when it's back. Meanwhile you can still manage the machine from its own command line:

```bash
sudo raptor ps                 # every server and what it's using
sudo raptor restart survival   # power actions
sudo raptor console survival   # the live console
```

See the [command line reference](/cli/reference/).

## If your machine goes offline

Its servers stop with it, and the Panel shows the node as offline. You can still see its servers and settings, as they were last reported, but nothing can be changed until it's back. When it reconnects, Wings starts the servers that were running and tells the Panel what happened while it was away.

## Your passkey and your machine

Some actions can destroy data or change what code runs: deleting a server, restoring a backup over it, wiping it, changing its game or startup command, trusting a new person's passkey. For those, the app asks your **passkey** to sign the exact request, and **Wings checks the signature itself**. So even someone who took over Raptor's servers couldn't do them without you. More in [How Raptor protects you](/security/how-raptor-protects-you/).

## Words Raptor uses

- **Org:** your team, with its members, nodes, and servers. Billing is per org.
- **Node:** a machine you've linked. It has a name, and an address like `1cdd002cd9a3.raptornodes.net` that follows its IP.
- **Server:** one game (or bot, or app) running on a node.
- **Egg:** the recipe a server is made from: what it runs, how to install it, its settings. Raptor reads Pterodactyl and Pelican eggs.
- **Short ID:** the end of a server's or node's ID, shown in the app's addresses and by `raptor ps`.
