# Security Policy

## Reporting a Vulnerability

Report privately through GitHub: **Security** tab → **Report a vulnerability**.

Include your macOS version, the Zubi version, reproduction steps, and the impact.
Please don't disclose publicly until it's fixed.

We'll respond as quickly as we can and keep you posted.

## Supported Versions

The latest release only. Update (**Check for Updates…** in the menu bar) before reporting.

## How your data is protected

- **Only through the relay, end to end encrypted.** Zubi's server listens on `127.0.0.1` only. A
  device reaches it through a relay (`relay.fa-krug.de` by default, or your own), in a Noise session
  keyed with that device's pairing code, which ends in Zubi's own server. The relay copies ciphertext
  and holds no key; see [docs/features/relay.md](docs/features/relay.md#invariants).
- **Per-device keys.** Every `/api` request needs `Authorization: Bearer <key>`, the key in the
  device's pairing code; only `/health` answers without one. A key is 32 random bytes from
  `SecRandomCopyBytes`, hex-encoded, stored in the login keychain, and compared in constant time.
  Removing a client or giving it a new code under Settings › Clients takes effect on the next session.
  Every change made through Zubi is listed under Settings › Activity.
- **Nothing exposed or writable by default.** Each reminder list, calendar and Notes folder is Off, Read
  or Read & Write in Settings, and that is checked on every request. An item that is Off answers 404,
  the same as one that doesn't exist; a write to one set to Read answers 403. Zubi can create and
  edit, never delete.

## Scope

Of particular interest:

- **Authentication** — any `/api` request served without the correct key, or a way to learn a key or
  pairing code.
- **The relay** — a relay, or anyone between it and the Mac or a device, reading or changing what a
  session carries, or reaching Zubi without a pairing code.
- **Exposure** — reading a reminder, event or note from a list, calendar or folder that is Off,
  including through an ID or name lookup.
- **Writes** — creating or changing anything in a list, calendar or folder that isn't set to Read &
  Write, moving an item into one that isn't, deleting anything, or changing a locked note.
- **Binding** — the server answering anywhere other than `127.0.0.1`.
- **Notes scripting** — anything that gets request input executed as JavaScript for Automation rather
  than passed as an argument, or widens what the Automation grant reaches.
- **TCC** — anything that widens what the Reminders, Calendar or Automation grants enable.
- **Signing and distribution** — the Developer ID release identity, notarization, and the in-app
  updater's signature check before it installs a release.

Out of scope: anything needing existing code execution or admin rights on the Mac or a paired
device, and what a relay operator can see by design: which addresses connect, when, and how much they
send.
