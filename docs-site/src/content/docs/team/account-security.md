---
title: Your account and security
description: Signing in without a password, passkeys, two-factor authentication, recovery codes, and your devices.
sidebar:
  order: 4
---

Raptor has **no passwords**. There's nothing to leak, reuse, or reset.

## Ways to sign in

- **A passkey** (best): your phone, computer, password manager, or a security key, unlocked with your fingerprint, face, or PIN. It can't be phished, and it already counts as two factors.
- **Google, Discord, or GitHub.**
- **An emailed code:** one email with a 6-digit code and a link; either works, once, for 10 minutes.

New to Raptor? Signing in for the first time makes your account.

## Passkeys

Add passkeys on **Settings → Security** with **Add a passkey**: name it, then confirm on your device. Add one on each device you use, so losing one isn't a problem. Removing one asks first, and warns you if it's your last. A removed passkey stays on your device or in your password manager until you delete it there too.

Your passkey does more than sign you in. Actions that could destroy data or change what runs on your machines (deleting a server, restoring a backup, changing its game, saving the firewall) are **signed by your passkey and checked by the machine itself**. So you need one to own nodes. Each node trusts the passkey you linked it with; add others, or other people's, on the node's **Settings → Trusted passkeys**.

Lost every passkey a node trusts? Re-pair one from the machine:

```bash
sudo raptor keys reset
```

## Two-factor authentication

Add an **authenticator app** (1Password, Google Authenticator, Authy…) on **Settings → Security**: **Set up** shows a QR code to scan, then asks for the 6-digit code the app shows. After that, signing in by email, Google, Discord, or GitHub also asks for a code from the app. Signing in with a passkey doesn't, since a passkey is already two factors.

Turning it on gives you **10 recovery codes**. Each works once, in place of an app code. They're shown once, so copy or download them before **I've saved them**. Make new ones any time on the same page (the old ones stop working). Turning the app off asks first.

## Confirm it's you

Sensitive changes (billing, adding a node, your passkeys, two-factor, your email) ask you to confirm it's you if you haven't in the last 5 minutes: with a passkey, an app code, or an emailed code.

## Devices

**Settings → Security → Devices** lists everywhere you're signed in, with when each was last active. Sign one out (this one too), or all of them, if you lost a device. Each asks first. Sessions end after 30 days unused, and 90 days at most.

Raptor emails you when something changes: a new device signing in, a passkey added or removed, two-factor turned on or off.

## Your email and linked accounts

On **Settings → General**: change your email (a code goes to the new address, and the old one is told), link or unlink Google, Discord, or GitHub, pick a theme, or delete your account. You can't remove your last way to sign in. Linking Discord also links you on Raptor's Discord server ([the Customer role](/help/get-help/#link-your-raptor-account)).
