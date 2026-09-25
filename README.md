# Raptor

**Raptor turns any Linux box into a reliable game server host. Run one command, manage everything from the web.**

Raptor is a hosted game server management platform. You bring your own Linux machine (a dedicated server, a VPS, a spare box at home), run a single install command, and it links to the web panel at [raptorpanel.net](https://raptorpanel.net). From there you create and manage game servers, Discord bots, and voice servers.

Raptor is compatible with Pterodactyl and Pelican eggs.

> **Status:** early development. Phase 0 (foundations) is complete. Phase 1 (Wings core) is in progress: eggs install and run from their real images, the Wings daemon runs under systemd with its local API, and the container runtime (networks, firewall isolation for install scripts, resource limits) is in place. Nothing here is usable yet. See the [roadmap](docs/ROADMAP.md).

## Components

| Component | What it is | Where it runs |
|---|---|---|
| **Panel** | Web app + API. Users, orgs, auth, billing, node enrollment. | Raptor's infrastructure (SaaS only) |
| **Wings** | The daemon. Runs servers in Docker, schedules, backups, SFTP, crash recovery. Keeps working when the Panel is unreachable. | The owner's Linux box |
| **`raptor` CLI** | Local tool for keeping servers up: status, power, console, logs, backup restore, `doctor`. | The owner's Linux box |

## Documentation

- [Product outline](docs/PRODUCT.md): what Raptor is and who it's for
- [Architecture](docs/ARCHITECTURE.md): how the pieces fit together
- [Wings](docs/WINGS.md): the daemon in detail
- [Panel](docs/PANEL.md): the SaaS control plane
- [Servers](docs/SERVERS.md): server model, states, ports, console, crash policy, deletion
- [Egg compatibility](docs/EGGS.md): Pterodactyl/Pelican egg support
- [Security model](docs/SECURITY-MODEL.md): threat model and security rules
- [Reliability and performance](docs/RELIABILITY.md): requirements and how we meet them
- [Decisions log](docs/DECISIONS.md): every locked decision and why
- [Roadmap](docs/ROADMAP.md)

## License

Raptor is licensed under the [GNU Affero General Public License v3.0](LICENSE). Contributions are accepted under the [Developer Certificate of Origin](CONTRIBUTING.md#developer-certificate-of-origin-dco).
