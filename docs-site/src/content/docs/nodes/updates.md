---
title: Updates
description: How Wings updates itself, how a bad update rolls back, and how to pin a version.
sidebar:
  order: 4
---

Raptor updates Wings on your nodes for you. **Game servers keep running through every update**: they belong to Docker, not to Wings.

## How an update happens

New releases go out in stages: a few nodes first, then a quarter, then everyone, with time to watch in between. A stage that goes badly is stopped before it reaches the rest.

On each node:

1. Wings downloads the release and checks its **signature** against Raptor's release key, built into Wings, and the file's checksum. Anything that doesn't check out isn't run.
2. The new version starts on trial, beside the old one.
3. Once it's healthy (its local API answering, Docker ready, and reconnected to the Panel) for 30 seconds, it takes over.
4. If it crashes, hangs, or can't reach the Panel within 5 minutes, it's **rolled back** automatically, and the old version carries on.

The node's Overview shows the Wings version, and alerts can tell you when a node updates or rolls back ([Alerts](/team/alerts/)).

## Update by hand

```bash
sudo raptor update -check     # what it would install
sudo raptor update            # install the newest release
sudo raptor update -version v0.6.0
```

`raptor update` follows the trial and prints the outcome. `-version` can go back to an older release, too.

## Pin a version or turn updates off

In `/etc/raptor/config.yml` on the machine:

```yaml
updates:
  automatic: false   # don't update from staged rollouts
  pin: "0.6.0"       # or install this version and stay on it
  channel: stable    # or beta, for pre-releases
```

Restart Wings after changing it (`sudo systemctl restart raptor-wings`; servers keep running). A pinned or non-automatic node is skipped by rollouts; `raptor update` still works by hand.
