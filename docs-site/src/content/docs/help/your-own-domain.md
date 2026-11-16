---
title: Use your own domain
description: Point a domain you own at a server, at a subdomain like play.example.com or the domain itself, and add an SRV record so Minecraft players don't type a port.
sidebar:
  order: 3
---

Players can connect with a name you own instead of the node's address. You add one or two records at whoever runs your domain's DNS: often where you bought the domain, or Cloudflare. Raptor doesn't need access to your domain.

The server's **Network** tab has **Use your own domain** under Shown address. Type your domain, pick a subdomain or the domain itself, and it shows the exact records for that server, ready to copy. This page explains them.

## Subdomain or the domain itself?

- **A subdomain** (`play.example.com`): the usual choice. It doesn't touch anything else on your domain, like a website, and you can have one per server (`smp.`, `creative.`, `rust.`).
- **The domain itself** (`example.com`, written `@` in most DNS settings): shorter for players. For Minecraft Java, an SRV record does it without touching your website. For other games, it points the whole domain at the node, so it replaces a website there.

## A subdomain

Add a **CNAME** record:

| Type | Name | Target |
|---|---|---|
| CNAME | `play` | the node's address, like `3f9a2c71b0de.raptornodes.net` |

The node's address is on its page, and in the dialog. A CNAME is better than an A record with the IP: the node's address follows the machine's IP when it changes (a home connection, a new provider), so your domain keeps working without you changing anything.

:::caution[Cloudflare: turn the proxy off]
In Cloudflare, set the record to **DNS only** (the grey cloud), not Proxied (the orange cloud). Cloudflare's proxy only carries websites: with it on, players can't connect. This is the most common mistake.
:::

Players now connect to `play.example.com:<port>`. For Minecraft Java, add an [SRV record](#srv-records-no-port-for-minecraft-java) too and they can leave the port off.

## The domain itself (@)

**Minecraft Java:** add only the [SRV record](#srv-records-no-port-for-minecraft-java), with the name `_minecraft._tcp` (that is, `_minecraft._tcp.example.com`). Minecraft looks for it first and goes straight to the node, so you don't need an A or CNAME record on the domain, and a website there keeps working.

**Other games:** a domain itself can't have a CNAME, so add an **A** record:

| Type | Name | IPv4 address |
|---|---|---|
| A | `@` | the node's public IPv4 address |

The dialog shows the address (hidden until you reveal it). If the machine's IP changes, update the record. On **Cloudflare** you can add a CNAME on `@` to the node's address instead: Cloudflare "flattens" it into the right IP and follows changes for you. Some other providers offer the same as an **ALIAS** or **ANAME** record.

## SRV records: no port for Minecraft Java

Normally players type the port after the name (`play.example.com:25601`). An SRV record tells Minecraft where the server really is, so players type only the name and the server can use any port.

When a player types `play.example.com`, Minecraft Java first asks DNS for `_minecraft._tcp.play.example.com`. If there's an SRV record there, it connects to the record's **target** on the record's **port**. If there isn't, it connects to `play.example.com` on port 25565.

| Field | What it is | Value |
|---|---|---|
| Type | | `SRV` |
| Name | `_minecraft._tcp` in front of the name players type | `_minecraft._tcp.play` (a subdomain) or `_minecraft._tcp` (the domain itself) |
| Priority | with several records, players try the lowest first | `0` |
| Weight | shares players between records with the same priority | `5` (any number, with one record) |
| Port | the server's port on the node | e.g. `25601` |
| Target | where the server is: a hostname, never an IP address | the node's address |

Some providers ask for the name in parts: **Service** `_minecraft`, **Protocol** `_tcp`, and **Name** `play` (or `@` for the domain itself). Cloudflare does this.

Only Minecraft Java and its proxies (Velocity, BungeeCord, Waterfall) read SRV records. Bedrock and most other games don't, so their players always type the port, unless the server uses the game's usual port (19132 for Bedrock).

Set the target to the node's address itself, not to your own `play.example.com`: an SRV target shouldn't be a CNAME (the standard forbids it, and some clients fail on it), while the node's address is a plain record.

## Set the shown address

On the server's **Network** tab, set **Shown address** to what players type: `play.example.com` or `example.com` with an SRV record, or with the port (`play.example.com:25601`) without one. The dialog's **Show … to players** button does it for you. The app then shows that name everywhere for the server, and keeps the node's address hidden.

## Check it

DNS changes can take from a minute to a few hours to reach everyone (usually minutes). To see what a name points at now:

```sh
nslookup play.example.com
nslookup -type=srv _minecraft._tcp.play.example.com
```

The first should end at the node's address (or its IP); the second should show your port and the node's address. Then join with the new name. If it doesn't work, run the **connection test** on the Network tab: if the test passes but the name doesn't, the records are wrong or haven't spread yet. See [Players can't connect](/help/players-cant-connect/).

## Where to add records

- **Cloudflare:** your domain → DNS → Records → Add record. Proxy status **DNS only**. For SRV, Cloudflare asks for the name (`_minecraft._tcp.play`), then priority, weight, port, and target.
- **Namecheap:** Domain List → Manage → Advanced DNS → Add new record. For SRV, the fields are Service, Protocol, Priority, Weight, Port, and Target; the host is `@` for the domain itself.
- **Porkbun:** Domain Management → your domain → DNS.
- **GoDaddy:** your domain → DNS → Add new record.
- **Amazon Route 53:** Hosted zones → your domain → Create record. An SRV value is one line: `0 5 25601 3f9a2c71b0de.raptornodes.net`.

If your domain uses another provider's name servers (Cloudflare's, say), add the records there, not at the registrar you bought it from.
