# Zubi

*Zubi* is Basque for "bridge": between your Mac and everything else.

<p align="center">
  <img alt="macOS 14 or later"
       src="https://img.shields.io/badge/macOS-14%2B-000000?style=flat&logo=apple&logoColor=white">
  <a href="LICENSE">
    <img alt="License: MIT"
         src="https://img.shields.io/badge/License-MIT-3DA639?style=flat"></a>
</p>

Zubi is a free menu bar app for your Mac. It shares the Reminders lists, calendars and Notes folders
you choose with your other devices, from anywhere: a Raspberry Pi display, a Linux or Windows
computer, a script, or an AI assistant. They read them and, where you allow it, add and edit
reminders, events and notes.

There are three parts, and you install two of them: **Zubi** on your Mac, the **`zubi`** client on each
device, and a **relay** in between that is already running. Each device's connection is encrypted end
to end with its own pairing code, so the relay passes along only what it can't read. There is no
account, and nothing on your Mac accepts connections from the internet.

**Documentation: <https://zubi.fa-krug.de/>**

## What you can do with it

- **Put your day on a screen.** A Raspberry Pi by the front door shows today's events and the
  shopping list, and updates the moment something changes (`zubi watch`).
- **Let an AI assistant manage your week.** Claude Desktop, Claude Code or any MCP-capable assistant
  can search your calendar, reminders and notes and, where you allow it, add events and tick off
  reminders (`zubi mcp`).
- **Script it.** A morning summary by email, a reminder count in your shell prompt, a plain-text copy
  of a Notes folder, with exact JSON from `--json`.
- **Reach it from Linux and Windows.** Your Apple calendars and reminders on computers Apple doesn't
  make apps for.

## On your Mac

You need macOS 14 Sonoma or later.

1. Download the latest `Zubi-Universal-<version>.zip` from
   [Releases](https://github.com/fa-krug/zubi/releases/latest), unzip it, move **Zubi** to your
   Applications folder and open it. It's signed and notarized by Apple; confirm once that it may open.
2. Settings opens on **Connection**: Zubi connects to the relay on its own and turns green, and the
   **pairing code** for your first device is right there.
3. **Choose what to share.** Under **Reminders**, **Calendars** and **Notes**, click **Grant Access**,
   then set each list, calendar and folder to **Read**, or to **Read & Write** if your devices may also
   add and edit items there. Everything starts **Off**.

That's it. Zubi starts with your Mac and keeps running in the menu bar, and keeps itself up to date.
The Mac has to be awake and logged in to answer.

## On each device

Install the client on Linux, a Raspberry Pi or macOS with one line:

```bash
curl -fsSL https://zubi.fa-krug.de/install.sh | sh
```

(or `brew install fa-krug/tap/zubi`; on Windows, Scoop: see [cli/README.md](cli/README.md#install)).
Then run it:

```bash
zubi
```

The first time, it asks for a pairing code: copy one from **Zubi › Settings › Connection** (or a
separate one per device under **Settings › Clients**) and paste it. On Linux it also sets up the
`zubi-client` service, which keeps the connection. After that, `zubi` shows the connection's status:

```text
✓ Paired as "Kitchen Pi", through wss://relay.fa-krug.de
✓ The zubi-client service keeps the connection
✓ Your Mac answers
```

Then:

```bash
zubi events --from 2026-11-02 --to 2026-11-06
zubi reminders add "Buy milk" --list Groceries --due 2026-11-02
zubi reminders --due overdue --json
zubi watch
```

[cli/README.md](cli/README.md) lists every command, flag and exit code.

## Your own relay

Zubi uses `wss://relay.fa-krug.de` unless you choose otherwise. To run your own, on a Raspberry Pi,
a server or a free hosting service, run `zubi relay` there, then turn on **Use my own relay** under
**Settings › Connection** and pair your devices again. [Running your own
relay](https://zubi.fa-krug.de/docs.html#own-relay) shows how.

## Privacy and security

- You decide per list, calendar and folder: **Off**, **Read**, or **Read & Write**. Anything Off
  answers as if it didn't exist; anything set to Read can't be changed through Zubi. Zubi never
  deletes anything.
- Every device gets its own pairing code, kept in your Mac's Keychain. Remove one under **Settings ›
  Clients** and that device is shut out, without touching the others.
- Each device's connection is encrypted end to end between it and your Mac. The relay sees which
  addresses connect and how much they send, never what.
- **Settings › Activity** lists everything created or edited through Zubi, and by which device.
- No analytics, no tracking.

The [privacy policy](https://zubi.fa-krug.de/privacy.html) lists every connection Zubi makes.

## Documentation

- [Documentation](https://zubi.fa-krug.de/docs.html): the app, every setting, the client, your own
  relay, updates and troubleshooting.
- [Command-line client](cli/README.md): install, commands, use from agents, and the relay.

## Help and feedback

Found a bug or have an idea? [Open an issue](https://github.com/fa-krug/zubi/issues/new/choose). Report
security problems privately, as described in [SECURITY.md](SECURITY.md).

## Contributing

Want to build Zubi yourself or change it? The [contribution guide](CONTRIBUTING.md) explains how the
project is put together, how to build and test it, and how to send a change.

## License

Zubi is free and open source under the [MIT License](LICENSE). The third-party software it includes is
listed in [NOTICE.md](NOTICE.md). Apple, macOS, Reminders, Calendar and Notes are trademarks of Apple
Inc.; Zubi is not affiliated with Apple.
