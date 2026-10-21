---
title: Members and access
description: Orgs, roles, invitations, and exactly what each member may do on each server.
sidebar:
  order: 1
---

An **org** is your team: its members, its nodes, and their servers. You can be in several orgs, and switch between them at the top of the sidebar. Billing is per org.

## Roles

| Role | Can |
|---|---|
| **Owner** | Everything, including billing, roles, and deleting the org. An org always has at least one. |
| **Admin** | Everything on every node and server: create and delete servers, link nodes, invite people, set alerts. Not billing or roles. |
| **Member** | Only what they've been given on each server (below). |

Owners change roles on **Settings → Members**. Each change asks first and says what it means; giving up your own owner role is one you can't undo yourself, since another owner has to give it back. Anyone can leave an org, except its last owner.

## Inviting people

On **Settings → Members**, under **Invite someone**, enter their email and a role. They get an email with a link, valid for 7 days, that works only when signed in with that address. Pending invitations are listed there to cancel.

## What members can do on a server

Members see only the servers they've been given access to. On a server's **Access** tab, admins tick what each member may do there:

| Permission | Lets them |
|---|---|
| **See the console** | Watch its console |
| **Send console commands** | Type commands into it (includes watching) |
| **Start and stop** | Start, stop, restart, and kill it |
| **See files** | Browse and download its files |
| **Change files** | Edit, upload, delete, and install add-ons |
| **Use SFTP** | Make temporary SFTP logins |
| **Backups** | Back it up and restore it |
| **Schedules** | Make and run schedules |
| **Startup settings** | Change its game settings and Workshop items |
| **Reinstall** | Reinstall it |

Creating and deleting servers, backup destinations, and node settings are for admins and owners.

## Dangerous actions and members

Some actions are signed with a passkey and checked by the node itself: restoring a backup over a server, wiping it, changing what it runs. An owner's passkey is trusted by the node from the start. For a member to do one of these on a node, an owner trusts their passkey there too, for just the actions they choose, on the node's **Settings → Trusted passkeys**. The node checks every signature itself, so nobody, Raptor included, can add trust without an owner's passkey. More in [How Raptor protects you](/security/how-raptor-protects-you/).

## Deleting an org

Owners can **Delete organization** from the org's **Settings**, once it has no nodes. Its members are removed, and its activity log is kept.
