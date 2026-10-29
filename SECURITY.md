# Security Policy

## Reporting a Vulnerability

Report privately through GitHub: **Security** tab → **Report a vulnerability**.

Include your macOS version, the Zubi version, reproduction steps, and the impact.
Please don't disclose publicly until it's fixed.

We'll respond as quickly as we can and keep you posted.

## Supported Versions

The latest release only. Update (**Check for Updates…** in the menu bar) before reporting.

## How the API is protected

- **Per-device keys.** Every `/api` route requires `Authorization: Bearer <key>`; only `/health`,
  `/openapi.json` and `/docs` answer without one. Each device gets its own key, Read or Read & Write; a
  Read key is refused every change, centrally, whatever an item's setting. Only the calendar feed also
  takes a key as `?key=`, and only a Read key, since URLs end up in logs. The key is 32 random bytes from `SecRandomCopyBytes`, hex-encoded, stored in the login
  keychain, and compared in constant time. Regenerating or deleting a key under Settings › Keys takes effect on the next
  request. Every change made through the API is listed under Settings › Activity.
- **Localhost by default.** A fresh install binds `127.0.0.1:8787`. Reaching it from another device
  means an SSH tunnel, or a deliberate choice under Settings › Server: a specific IP (such as a Tailscale address)
  or all interfaces.
- **Plain HTTP.** Zubi does not terminate TLS. Off localhost, the key and the data travel unencrypted
  unless the network itself is encrypted (SSH tunnel, Tailscale, a VPN). This is by design, not a
  vulnerability.
- **Nothing exposed or writable by default.** Each reminder list, calendar and Notes folder is Off, Read
  or Read & Write in Settings, and that is checked on every request. An item that is Off answers 404,
  the same as one that doesn't exist; a write to one set to Read answers 403. The API can create and
  edit, never delete.

## Scope

Of particular interest:

- **Authentication** — any `/api` request served without the correct key, or a way to learn the key.
- **Exposure** — reading a reminder, event or note from a list, calendar or folder that is Off,
  including through an ID or name lookup.
- **Writes** — creating or changing anything in a list, calendar or folder that isn't set to Read &
  Write, moving an item into one that isn't, deleting anything, or changing a locked note.
- **Binding** — the server listening anywhere other than what Settings › Server shows.
- **Notes scripting** — anything that gets request input executed as JavaScript for Automation rather
  than passed as an argument, or widens what the Automation grant reaches.
- **TCC** — anything that widens what the Reminders, Calendar or Automation grants enable.
- **Signing and distribution** — the Developer ID release identity, notarization, and the in-app
  updater's signature check before it installs a release.

Out of scope: anything needing existing code execution or admin rights on the Mac, and the
consequences of choosing a non-localhost bind on an untrusted network.
