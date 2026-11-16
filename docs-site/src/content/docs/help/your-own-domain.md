---
title: Use your own domain
description: Point a domain you own, like play.example.com, at a server, with the records to add at your DNS provider.
sidebar:
  order: 3
---

Players can connect with a name you own, like `play.example.com`, instead of the node's address. You add one or two records at whoever runs your domain's DNS. Raptor doesn't need access to your domain.

The server's **Network** tab has **Use your own domain**: type the name you want, and it shows the exact records for that server, ready to copy. The rest of this page explains them.

## 1. Point the name at the node

Add a **CNAME** record:

| Type | Name | Target |
|---|---|---|
| CNAME | `play` (that is, `play.example.com`) | the node's address, like `3f9a2c71b0de.raptornodes.net` |

The node's address is on its page, and on the server's Network tab. Use a CNAME rather than an A record with the IP: the node's address follows the machine's IP when it changes (a home connection, a new provider), so your domain keeps working without you changing anything.

A CNAME can't go on the bare domain (`example.com`), only on a name under it (`play.example.com`). If you want the bare domain, use an A record with the node's IP, and update it yourself if the IP changes.

:::caution[Cloudflare: turn the proxy off]
In Cloudflare, set the record to **DNS only** (the grey cloud), not Proxied (the orange cloud). Cloudflare's proxy only carries websites: with it on, players can't connect. This is the most common mistake.
:::

## 2. Minecraft Java: add an SRV record (optional)

With only the CNAME, players type the port: `play.example.com:25566`. An **SRV** record lets them type just `play.example.com`. Only Minecraft Java (and proxies like Velocity and BungeeCord) read SRV records. Bedrock and most other games don't, so players of those always add the port, unless the server uses the game's usual port.

| Field | Value |
|---|---|
| Type | SRV |
| Name | `_minecraft._tcp.play` (some providers split this into Service `_minecraft`, Protocol `_tcp`, Name `play`) |
| Priority | `0` |
| Weight | `5` |
| Port | the server's port |
| Target | the node's address |

## 3. Set the shown address

On the server's **Network** tab, set **Shown address** to the name players should type: `play.example.com` with an SRV record, or `play.example.com:25566` without one. The app then shows that name everywhere for the server, and keeps the node's address hidden.

## 4. Check it

DNS changes can take from a minute to a few hours to reach everyone (usually minutes). To see what the name points at now:

```sh
nslookup play.example.com
nslookup -type=srv _minecraft._tcp.play.example.com
```

The first should end at the node's address. Then join the server with the new name. If it doesn't work, run the **connection test** on the Network tab: if the test passes but the name doesn't, the records are wrong or haven't spread yet. See [Players can't connect](/help/players-cant-connect/).

## Where to add records

- **Cloudflare:** your domain → DNS → Records → Add record. Proxy status **DNS only**.
- **Namecheap:** Domain List → Manage → Advanced DNS → Add new record. For SRV, the fields are Service, Protocol, Priority, Weight, Port, and Target.
- **Porkbun:** Domain Management → your domain → DNS.
- **GoDaddy:** your domain → DNS → Add new record.
- **Amazon Route 53:** Hosted zones → your domain → Create record. An SRV value is one line: `0 5 25566 3f9a2c71b0de.raptornodes.net`.

If your domain uses another provider's name servers (Cloudflare's, say), add the records there, not at the registrar you bought it from.
