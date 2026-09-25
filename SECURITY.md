# Security Policy

Raptor Wings runs as root on its users' machines, so we take security reports seriously.

## Reporting a vulnerability

**Don't open a public GitHub issue for security problems.**

- Email: **security@raptorpanel.net**
- Or use GitHub's private vulnerability reporting on this repository

Please include the affected component (Panel, Wings, CLI, installer), steps to reproduce, and the impact you believe it has.

## What to expect

- Acknowledgement within **72 hours**
- An initial assessment within **7 days**
- Fixes for critical issues are prioritized over all other work
- Credit in the release notes if you'd like it

## Scope

In scope:
- The Panel (raptorpanel.net) and its API
- Wings, the `raptor` CLI, and the install script
- The Panel ↔ Wings tunnel protocol, enrollment, and certificates
- Egg processing in Wings (variable substitution, config file parsing, file paths)

Out of scope:
- Vulnerabilities inside game servers themselves, or in third-party eggs and Docker images
- Denial of service through volume alone
- Problems that require root access on the node (root already controls the node)

## Safe harbor

We won't pursue legal action against good-faith research that follows this policy, avoids privacy violations and data destruction, and doesn't touch other users' nodes or data.

See [docs/SECURITY-MODEL.md](docs/SECURITY-MODEL.md) for the threat model.
