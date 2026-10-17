---
title: Players can't connect
description: Find out why players can't join a server, and fix it for your provider or your home router.
sidebar:
  order: 2
---

Open the server's **Network** tab and choose **Test connection**. Raptor connects to each of the server's ports from the internet while the node checks what the game is listening on, so the result says which side is wrong. It takes a few seconds.

## What the result means

- **Players can reach this server.** The port is open. If a player still can't join, check they're using the address the test shows.
- **The server isn't running.** Start it, wait until it's up, then test again.
- **Nothing is listening on port … yet.** If the server just started, give it a minute. If it's been running a while, check its console for errors.
- **The game is listening on port …, not ….** The game opened a different port from the one players are given. Set the port in its config to the server's port (for Minecraft, `server-port` in `server.properties`), then restart it.
- **The game only accepts connections from its own machine.** It's listening on 127.0.0.1. Clear the address it binds to (for Minecraft, leave `server-ip` in `server.properties` empty), then restart it.
- **Players can't reach this server.** The game is listening, but connections don't get through. Something in front of the node is blocking the port, almost always your provider's firewall. Open it with the steps below.
- **The game is listening, over UDP.** UDP can't be tested from outside unless the game answers. If players can't join, the port is probably blocked: open it for UDP with the steps below.

The test picks your provider from the node's address and shows the matching steps under **How to open the port**. They're here too.

## Open the port at your provider

Use the server's port, and the protocol the test names (TCP, UDP, or both).

### Hetzner

- **Cloud:** Firewalls → your firewall → Inbound rules → Add rule: the protocol, the port, source Any IPv4 and Any IPv6. Make sure the firewall is applied to this server.
- **Dedicated (Robot):** your server → Firewall → add a rule allowing the protocol and port, and save.

### OVHcloud

OVH doesn't block ports by default. If you turned on the Edge Network Firewall (Network → Public IP addresses → your IP → … → Edge Network Firewall configuration), add an Authorize rule for the port before any deny rule.

### Oracle Cloud

Networking → Virtual cloud networks → your VCN → Security Lists → the default list → Add Ingress Rules: source `0.0.0.0/0`, the protocol, destination port.

Oracle's own images also come with firewall rules on the machine. If the test still fails, run `sudo raptor doctor` on the node: it says what to change.

### AWS

EC2 → Instances → this instance → Security → its security group → Edit inbound rules. Add a rule for each protocol: the port, source `0.0.0.0/0` (and `::/0` for IPv6). Save.

### Google Cloud

VPC network → Firewall → Create firewall rule: direction ingress, targets this VM (all instances, or its network tag), source `0.0.0.0/0`, the protocol and port. Create.

### Azure

This VM → Networking → Add inbound port rule: destination port ranges the port, protocol Any, action Allow. Add.

### DigitalOcean

Networking → Firewalls. If a firewall applies to this droplet, add inbound rules for the protocol and port from all IPv4 and IPv6. Droplets without one have nothing blocked.

### Akamai (Linode)

This Linode → Network → Firewalls. In its firewall, add inbound rules accepting the protocol and port from all addresses.

### Vultr

Network → Firewall → the group this server is in → add IPv4 (and IPv6) rules for the protocol and port, source anywhere.

### At home

1. Open your router's settings page (often `192.168.0.1` or `192.168.1.1`) and find Port Forwarding (sometimes called NAT, Virtual Server, or Gaming).
2. Forward the port, for the protocol the test names, to this computer's local address.
3. If the internet address your router shows isn't the one the test shows, your internet provider shares one address between customers (CGNAT), and forwarding can't work. Ask them for a public IP address.

### Another provider

In your provider's control panel, look for Firewall, Security group, or Network rules, and allow the protocol and port from anywhere.

## Firewalls on the node

Firewalls on the node itself, like ufw and firewalld, rarely matter: game servers' ports go around them. The node's **System** tab shows every firewall rule on the machine and who made it. If you're unsure, run `sudo raptor doctor` on the node.
