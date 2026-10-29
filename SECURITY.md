# Security Policy

## Reporting a Vulnerability

Report privately through GitHub: **Security** tab → **Report a vulnerability**.

Include your macOS version, the Zubi version, reproduction steps, and the impact.
Please don't disclose publicly until it's fixed.

We'll respond as quickly as we can and keep you posted.

## Supported Versions

The latest release only. Update (**Check for Updates…** in the menu bar) before reporting.

## How the API is protected

- **Bearer key.** Every `/api` route requires `Authorization: Bearer <api-key>`; only `/health` answers
  without it. The key is 32 random bytes from `SecRandomCopyBytes`, hex-encoded, stored in the login
  keychain, and compared in constant time. **Regenerate** in Setup replaces it and restarts the
  server.
- **Localhost by default.** A fresh install binds `127.0.0.1:8787`. Reaching it from another device
  means an SSH tunnel, or a deliberate choice in Setup: a specific IP (such as a Tailscale address)
  or all interfaces.
- **Plain HTTP.** Zubi does not terminate TLS. Off localhost, the key and the data travel unencrypted
  unless the network itself is encrypted (SSH tunnel, Tailscale, a VPN). This is by design, not a
  vulnerability.
- **Nothing exposed by default.** Only the reminder lists, calendars and Notes folders ticked in Setup
  are served, and that is checked on every request. A hidden item answers 404, the same as one that
  doesn't exist.

## Scope

Of particular interest:

- **Authentication** — any `/api` request served without the correct key, or a way to learn the key.
- **Exposure** — reading a reminder, event or note from a list, calendar or folder that isn't ticked,
  including through an ID or name lookup.
- **Binding** — the server listening anywhere other than what Setup shows.
- **Notes scripting** — anything that gets request input executed as JavaScript for Automation rather
  than passed as an argument, or widens what the Automation grant reaches.
- **TCC** — anything that widens what the Reminders, Calendar or Automation grants enable.
- **Signing and distribution** — the Developer ID release identity, notarization, and the in-app
  updater's signature check before it installs a release.

Out of scope: anything needing existing code execution or admin rights on the Mac, and the
consequences of choosing a non-localhost bind on an untrusted network.
