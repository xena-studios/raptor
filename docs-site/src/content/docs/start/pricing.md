---
title: Pricing and billing
description: One price per machine, the free first month, backup storage, invoices, and what happens if you stop paying.
sidebar:
  order: 3
---

Raptor costs **$12 a month for each node** (machine) linked to your org. Every feature, unlimited servers, unlimited people. There are no limits on memory, CPU, or players: it's your hardware.

## The free month

Your org's first node is free for its first month.

- Start it from **Settings → Billing → Start your free month**, or when you go to connect your first node.
- A card is needed to start, through Polar's checkout. Nothing is charged until the month is up.
- Linking a **second** node ends the free month early, and the app says so before you do.
- The free month is once per org.

## How you're billed

- **Per node, whether it's online or not.** A node stops counting only when you remove it from the Panel (its servers keep running on the machine).
- Adding or removing a node mid-month is **prorated** on the next invoice.
- Billing is through **Polar**, who handles VAT and sales tax. Raptor never sees your card.

## Managing your plan

Owners manage billing on **Settings → Billing**:

- **Cards:** add one (Polar's form, framed in the page), make one the default, or remove one. Polar won't remove the card a plan still needs.
- **Charges:** your recent charges, each with an invoice to download.
- **Cancel:** the plan ends at the end of the period. You can take a cancellation back until then.
- **Billing address, tax ID, and receipt email:** in Polar's portal, linked at the bottom of the page.

Changing cards and cancelling ask you to confirm it's you first. Members and admins can see the plan, but only owners can change it.

## Raptor Backup Storage

Offsite backups with nothing to set up. See [Backups](/servers/backups/#raptor-backup-storage).

- **10 GB included** for every node on a plan, shared across the org.
- More is **$12 per TB a month**, billed by the GB ($0.012 per GB).

## If you stop paying

- A failed payment gives you **7 days**. Owners are emailed when it fails, and every page of the org says so.
- After that, the Panel goes **read-only** for your nodes: you can look, but not change anything, and you can't link new nodes.
- **Your servers keep running.** Raptor never stops a game over billing, and the `raptor` command on each machine keeps working.
- Paying again (a new card, or paying the invoice) brings everything back at once.
- Backups in Raptor Backup Storage are kept 30 days after a plan ends, with reminder emails before they're deleted.
