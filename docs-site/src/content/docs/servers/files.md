---
title: Files
description: The file manager, the editor, uploads, downloading links, importing from another host, archives, the trash, and SFTP.
sidebar:
  order: 3
---

Every server has a file manager on its **Files** tab: a folder tree, the list, and an editor, all in the browser. Seeing files needs the **See files** permission; changing them **Change files**.

## Moving around

- **Open** a folder or file with a click (or a double click: change it in the file manager's settings, the gear in its toolbar).
- **Select** with the checkboxes, or with Ctrl or Shift and a click. The bar above the list then offers **Compress** and **Delete**.
- **Right-click** a file, a folder, or empty space for everything you can do there.
- **Search** filters the folder you're in.
- **Keyboard shortcuts** are behind the keyboard button in the toolbar.
- The file manager can go **full screen**, or **into its own window** with the button at the end of its toolbar.

## Editing

Files up to 4 MB open in the editor, with syntax highlighting for configs and scripts. Save with Ctrl+S (Cmd+S on a Mac). If you leave with unsaved changes, they're kept in the browser as a draft for when you come back.

## Uploading and downloading

- **Upload** a file, or drop files anywhere on the file manager. Each can be up to 1 GB; uploads resume if your connection drops.
- **Download** a file from its menu, up to 1 GB.
- Anything bigger: use [SFTP](#sftp).

## Download from a link

**From URL** in the toolbar downloads a file straight onto the machine, so a big modpack or world never goes through your computer. Paste the link, check the name it's saved as, and choose **Download**. Its progress shows above the file list, where you can cancel it.

- `http://` and `https://` links, up to 50 GB and what the server has left.
- The machine only downloads from the public internet: links to your home network, the machine itself, or a cloud provider's internal addresses are refused, even through a redirect.
- It never replaces a file: pick another name if one's there.

## Import from another host

**Import** in the toolbar copies a server's files in over SFTP: from an old Pterodactyl or Pelican panel, a game host, or a shared host.

1. Enter the **host** and **port**, the **username**, and the **password** (or a private key), and optionally the **folder** to copy from. Pterodactyl and Pelican show these on the server's Settings page, as SFTP details.
2. Raptor shows the host's **key fingerprint**. If your old host shows one, check it's the same: then nothing in between can see your files or password.
3. Choose **Import**. Progress shows above the file list.

**Nothing already in the server is replaced**: files with the same name are skipped and counted, as are files the game protects and links. The password is kept in the machine's memory for that one import, never stored; if the machine restarts mid-import, start it again.

Moving from Pterodactyl on the **same** machine? [`raptor import pterodactyl`](/install/from-pterodactyl/) brings the whole server across, settings and all.

## Archives

- **Compress** selected files into `.tar.gz` (the default), `.zip`, `.tar.zst`, or `.tar`.
- **Extract** `.zip`, `.tar`, `.tar.gz`, `.tgz`, `.tar.bz2`, `.tar.zst`, and single `.gz` files from their menu. Extracting replaces files with the same names. (`.rar`, `.7z`, and `.xz` aren't supported.)

Both run on the machine; the file appears when it's done.

## The trash

Deleting in the file manager **always moves to the trash**, at the bottom of the folder tree. From there you can **Restore** what you deleted, **Purge** it for good, or **Empty trash**.

- The trash keeps things for **7 days**, then empties itself.
- It still counts toward the server's disk limit until it's purged.
- Deletes over SFTP, and by the game itself, don't go through the trash.

## Protected files

A game's recipe can protect files, such as its server jar, so they can't be read or changed from the file manager or SFTP. They're listed with a lock. A folder holding a protected file can't be moved or deleted.

## SFTP

For big transfers, or an SFTP client like FileZilla or WinSCP, use **SFTP** in the file manager's toolbar (the **Use SFTP** permission):

1. Choose how long the login lasts (an hour, a day, 7 days, or 30 days) and **Turn on SFTP**. Raptor shows a **host**, **port**, **username** (starting `t-`), and a **password** to copy. The password is shown once.
2. Connect with any SFTP client. The login only reaches this server's files.
3. Turn it off when you're done, or let it run out: sessions end with it.

The first time you connect, your client shows the node's **host key fingerprint**: check it matches the one in the SFTP dialog.

- SFTP is on only while someone has a login on the node, and admins can stop it on a node entirely (its **Settings** tab).
- The username is a temporary login, not an account: it works only until it expires.
- Deleting over SFTP is permanent: there's no trash.
