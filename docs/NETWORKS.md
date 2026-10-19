# Networks

Docker networks for servers, managed from the app: separate communities on one node kept apart, proxies that reach only their own backends, and the network side of [databases](DATABASES.md). Phase 1 is built: networks, isolation, servers on them, the node's Networks tab, the server's Networks card, and the network picker in Deploy server. Rules between networks are next ([ROADMAP.md](ROADMAP.md#networks-and-databases), DECISIONS #300–#302).

## Today

Every server on a node is on one bridge, `raptor_nw` (`raptor0`), and can reach every other server's ports, which proxies like Velocity rely on. Install containers have their own (`raptor_install`), cut off from the host and private ranges ([WINGS.md](WINGS.md#networking)). There's no way to keep two groups of servers on one machine from reaching each other.

## The model

- **Network:** a Docker bridge Wings creates and owns (`raptor_net_<id>`, bridge `rn<12 hex>`, labelled `raptor.wings.managed=true` and `raptor.wings.network=true`), with a name ("Network A"), a subnet, and settings. A node has up to 32.
- **The default network** is today's `raptor_nw`. Existing servers stay on it, and new servers go on it unless another is picked, so nothing changes until an owner makes a network.
- **Primary network:** every server has exactly one. Its address there is the server's, and allocations bound to `127.0.0.1` are published on that network's gateway.
- **More networks:** a server can also join others, so a proxy can sit on its own network and its backends' at once.
- **Database instances** join networks too ([DATABASES.md](DATABASES.md)): a server can use an instance only if they share a network.

### A network's settings

| Setting | What it does |
|---|---|
| **Name** | What it's called in the app (1 to 40 characters, unique on the node). Its servers find each other as `<server name>.raptor` ("Survival SMP" is `survival-smp.raptor`) and `<first 8 of the server's ID>.raptor`, which a rename doesn't change. |
| **Subnet** | Picked like `raptor_nw`'s (the first free `/16`), or set by the owner: a private range from `/16` to `/28`, refused if it overlaps an interface, a route, another Docker network, or Raptor's own. |
| **Internet access** | On (the default) or off: off makes it an `internal` network with no route out, for backends that should only talk to their proxy. Their allocations still accept players, because published ports are forwarded in by the host. |
| **IPv6** | Off by default; on gives the network a ULA `/64` too, the same every time (from its ID). |
| **MTU** | Docker's default unless set (1280 to 9000); set lower under a VPN or a provider that needs it. |
| **Reach the host** | On by default (servers can reach services on the node, as now); off drops their connections to the machine itself. Docker's DNS still works: it answers inside the container. So can other servers' published ports through the node's own address, on machines where Docker's proxy answers them on the host; a proxy that needs a backend should join its network instead. |

Changing the subnet, internet access, IPv6, or MTU recreates the network on Docker, so it's refused while a server on it runs. The name and Reach the host change in place.

The driver is always `bridge`. `macvlan` and `ipvlan` (servers on the LAN with their own addresses) would be a later option, since they bypass Raptor's firewall table.

## Isolation between networks

- **Bridges don't forward to each other:** Docker drops traffic between its bridges already (`DOCKER-ISOLATION-STAGE-*`), and Raptor's own table repeats it in `inet raptor` (the `between` chain) so it doesn't depend on Docker's chains surviving. Ports published to the internet stay reachable from every network (through the node's address), since anyone can reach them anyway.
- **The gateway loophole closed:** a port published on one network's gateway (an allocation on `127.0.0.1`) is reachable from anything that can reach the host, including other networks' servers; the runtime e2e test shows Docker alone lets it through. Raptor's table drops connections to each server network's gateway (`raptor0`'s too) that come in on any other interface, before Docker's port translation sees them.
- **Names stay inside:** each network has Docker's own DNS, so `survival.raptor` resolves only on the networks Survival is on.

### Rules between networks

For what should cross, an owner adds an **allow rule**: from a network (or one server on it) to a network (or one server), on a protocol and ports ("Network A may reach Lobby on TCP 25565"). A rule is one way; replies to the connections it allows get back, and nothing is allowed the other way. There are no deny rules: everything not allowed is already blocked. Rules are IPv4 only, and can't name the default network: put the servers on a network of their own first.

- **Servers' fixed addresses:** a rule naming one server has to match its address, and Docker hands out a different one at each start. A network's range is split in half: Docker hands out the lower half (its `IPRange`), and each server is pinned to the first free address in the upper half the first time it starts there (`server_addresses`), so it keeps it from then on. The network's broadcast address is never used. A rule naming a server that hasn't started on the network yet lets nothing through until it has. Moving a server off a network forgets its address there at its next start; changing a network's subnet forgets them all. Networks made before rules have no `IPRange`; their Docker addresses start at the bottom, so they'd reach the upper half only past half the range, and Docker refuses a duplicate address rather than sharing it.
- **Three layers, one decision:** the rule goes in `inet raptor`'s `between` chain, which tracks connections: replies get back, and a new connection either way that no rule allows is dropped. Docker also blocks traffic between its bridges in its own iptables rules, with a drop per container in `raw PREROUTING` and a drop into each bridge in the filter table, where Raptor's accept can't override it. So Wings mirrors each rule into a chain named `RAPTOR-BETWEEN` in both tables, jumped to first from `raw PREROUTING` and from `DOCKER-USER` (the chain Docker keeps for its users), with the reverse direction in `raw` for replies, since `raw` runs before connection tracking. Traffic crosses only where Raptor's table and Docker's chains both let it. The jumps are checked every minute and put back if Docker's restart removed them. On Docker's nftables firewall backend there's no `DOCKER-USER`; Wings logs that rules can't let anything through there.
- Stored in `kv` (`networks.rules`), replaced as a whole by `node.network.rules`, and returned by `node.networks` with each network's servers' addresses. Deleting a network removes its rules.

## In the app

The node's **Networks** tab (admins and owners; `web/src/components/node-networks.tsx`). Making a network takes only a name. Internet access, Reach the machine, IPv6, the subnet, and the MTU wait under **Advanced**, which is open when editing a network that has any of them changed. The tab has:

- every Raptor network, with its subnet, its servers and database instances, and its settings;
- **New network**, and editing or deleting one (a network with servers on it can't be deleted; move them first);
- the rules between networks;
- the machine's other Docker networks, read-only (Pterodactyl's, ones made by hand), so the owner can see what's there. Raptor never changes or removes a network it didn't make.

A server's **Network** tab gets its primary network and the others it's on. Changing them takes effect at its next start, which the app says. Deploy server asks which network a new server goes on when the node has any. A server on the host's network is on no other.

Changing networks and rules is for admins and owners, unsigned (it deletes nothing and runs no new code), and every change is in the node's activity.

## In Wings

- `internal/wings/networks`: creates, updates (settings Docker can't change in place, like the subnet, mean recreating the network with its servers stopped, so it's refused while any run), and deletes networks (refused while a server is on one); reconciles them at start, before the firewall and servers; lists every Docker network for the read-only view. Stored in the `networks` table; a server's are the `networks` column, a JSON list of IDs.
- Containers are created on the primary network (with the default route) and connected to the others before they start; the server's allocations, its `127.0.0.1` binding, and `{{config.docker.interface}}` follow the primary network.
- Commands: `node.networks` (a read: the networks with their servers, and every Docker network), `node.network.save` (no ID creates; internet access and Reach the host default to on), `node.network.delete`, `node.network.rules`, and `server.networks` (a server's list, primary first; empty is `raptor_nw`). `server.create` takes `networks` too. `server.update` never changes them, so a member who may change a server's startup can't move it between networks. All are admin-only in the Panel and unsigned. Capability `networks`; each server's reported config has `networks` and `aliases`.
- Later: `raptor doctor` checking each network exists with the settings Raptor expects, and that the `RAPTOR-BETWEEN` jumps are in place.

## Phases

Networks come before databases, since instances attach to them:

1. **Networks:** the model, settings, isolation and the gateway rule, the Networks tab, servers' primary and extra networks, names on each network.
2. **Rules between networks.** Built: Wings, the Rules between networks card on the Networks tab, and servers' fixed addresses shown there and on their Network tab.
3. **Databases** on networks ([DATABASES.md](DATABASES.md#phases)).
