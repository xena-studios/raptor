# Networks

Docker networks for servers, managed from the app: separate communities on one node kept apart, proxies that reach only their own backends, and the network side of [databases](DATABASES.md). This is the plan; nothing here is built yet ([ROADMAP.md](ROADMAP.md#networks-and-databases), DECISIONS #300).

## Today

Every server on a node is on one bridge, `raptor_nw` (`raptor0`), and can reach every other server's ports, which proxies like Velocity rely on. Install containers have their own (`raptor_install`), cut off from the host and private ranges ([WINGS.md](WINGS.md#networking)). There's no way to keep two groups of servers on one machine from reaching each other.

## The model

- **Network:** a Docker bridge Wings creates and owns (labelled `raptor.wings.managed=true`, `raptor.network=<id>`), with a name ("Network A"), a subnet, and settings. A node has any number.
- **The default network** is today's `raptor_nw`. Existing servers stay on it, and new servers go on it unless another is picked, so nothing changes until an owner makes a network.
- **Primary network:** every server has exactly one. Its address there is the server's, and allocations bound to `127.0.0.1` are published on that network's gateway.
- **More networks:** a server can also join others, so a proxy can sit on its own network and its backends' at once.
- **Database instances** join networks too ([DATABASES.md](DATABASES.md)): a server can use an instance only if they share a network.

### A network's settings

| Setting | What it does |
|---|---|
| **Name** | What it's called in the app; its servers find each other as `<server name>.raptor` on it. |
| **Subnet** | Picked like today's (the first free `/16`, or a `/24` for small networks), or set by the owner, refused if it overlaps an interface, a route, or another network. |
| **Internet access** | On (the default) or off: off makes it an `internal` network with no route out, for backends that should only talk to their proxy. Their allocations still accept players, because published ports are forwarded in by the host. |
| **IPv6** | Off by default; on gives the network a ULA `/64` too. |
| **MTU** | Defaults to the host's; set lower under a VPN or a provider that needs it. |
| **Reach the host** | On by default (servers can reach `127.0.0.1`-style services on the node, as now); off drops their traffic to the host itself except DNS. |

The driver is always `bridge`. `macvlan` and `ipvlan` (servers on the LAN with their own addresses) would be a later option, since they bypass Raptor's firewall table.

## Isolation between networks

- **Bridges don't forward to each other:** Docker drops traffic between its bridges already (`DOCKER-ISOLATION-STAGE-*`), and Raptor's own table repeats it in `inet raptor` so it doesn't depend on Docker's chains surviving.
- **The gateway loophole closed:** a port published on one network's gateway (an allocation on `127.0.0.1`) is reachable from anything that can reach the host, including other networks' servers. Raptor's table allows those ports only from that network's subnet and the host itself.
- **Names stay inside:** each network has Docker's own DNS, so `survival.raptor` resolves only on the networks Survival is on.

### Rules between networks

For what should cross, an owner adds an **allow rule**: from a network (or one server on it) to a network (or one server), on a protocol and ports ("Network A may reach Lobby on TCP 25565"). Rules are in `inet raptor`, applied with the firewall's other rules, and listed on the Networks page with what they let through. There are no deny rules: everything not allowed is already blocked.

## In the app

The node's **Networks** tab (admins and owners):

- every Raptor network, with its subnet, its servers and database instances, and its settings;
- **New network**, and editing or deleting one (a network with servers on it can't be deleted; move them first);
- the rules between networks;
- the machine's other Docker networks, read-only (Pterodactyl's, ones made by hand), so the owner can see what's there. Raptor never changes or removes a network it didn't make.

A server's **Network** tab gets its primary network and the others it's on. Changing them takes effect at its next start, which the app says. Deploy server asks which network a new server goes on when the node has more than one.

Changing networks and rules is for admins and owners, unsigned (it deletes nothing and runs no new code), and every change is in the node's activity.

## In Wings

- `internal/wings/networks`: creates, updates (settings Docker can't change in place, like the subnet, mean recreating the network with its servers stopped, so the app refuses that while any run), and deletes networks; reconciles them at start like `raptor_nw` today; lists every Docker network for the read-only view.
- Containers are created on the primary network and connected to the others before they start; the server's allocations and its `127.0.0.1` binding follow the primary network.
- `node.networks`, `node.network.save`, `node.network.delete`, `node.network.rules`, and `server.update`'s `networks` field; capability `networks`.
- `raptor doctor` checks each network exists with the subnet and settings Raptor expects, and that no two overlap.

## Phases

Networks come before databases, since instances attach to them:

1. **Networks:** the model, settings, isolation and the gateway rule, the Networks tab, servers' primary and extra networks, names on each network.
2. **Rules between networks.**
3. **Databases** on networks ([DATABASES.md](DATABASES.md#phases)).
