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
you choose over a small, password-protected web interface (an HTTP API), so your other devices can
read them, and, where you allow it, add and edit reminders, events and notes: a Raspberry Pi display,
Home Assistant, a Linux or Windows computer, a script, or an AI agent.

Apple keeps this data on your Mac and in iCloud, with nothing a non-Apple device can ask. Zubi runs
where the data already is and hands out exactly what you allow, item by item. There is no account and no Zubi
server: your data goes from your Mac straight to your own devices.

**Documentation: <https://fa-krug.github.io/zubi/>**

## What you can do with it

- **Put your day on a screen.** A Raspberry Pi by the front door shows today's events and the
  shopping list, and updates the moment something changes.
- **Automate your home.** Home Assistant or Node-RED reacts when a reminder is overdue.
- **Let an AI assistant manage your week.** Claude Desktop, Claude Code or any MCP-capable assistant
  can search your calendar, reminders and notes and, where you allow it, add events and tick off
  reminders, through the `zubi` command.
- **Subscribe from any calendar app.** Thunderbird, GNOME Calendar or a wall display can subscribe to a
  shared calendar by link.
- **Script it.** A morning summary by email, a reminder count in your shell prompt, a plain-text copy
  of a Notes folder.
- **Reach it from Linux and Windows.** Your Apple calendars and reminders on computers Apple doesn't
  make apps for.

## Install

You need a Mac with macOS 14 Sonoma or later.

1. Download the latest `Zubi-Universal-<version>.zip` from
   [Releases](https://github.com/fa-krug/zubi/releases/latest).
2. Unzip it and move **Zubi** to your Applications folder.
3. Open it. macOS asks once whether to open an app from the internet; Zubi is signed and notarized
   by Apple, so confirm. Its bridge icon appears in the menu bar.

Zubi keeps itself up to date: once a day it checks for a new version, shows what changed, and installs
it when you say so.

## Get started

Three steps, about three minutes:

1. **Choose what to share.** Click the menu bar icon, then **Settings…**. Under **Reminders**,
   **Calendars** and **Notes**, click **Grant Access**, then set each list, calendar and folder to
   **Read**, or to **Read & Write** if other devices may also add and edit items there. Everything
   starts **Off**: nothing is shared until you choose.
2. **Copy the key.** Under **Keys**, click **Copy Command**. Later, give each device its own key
   there, either **Read** or **Read & Write**.
3. **Try it.** Paste the command into Terminal. Your shared events come back as JSON:

   ```bash
   curl -H "Authorization: Bearer <key>" http://127.0.0.1:8787/api/events
   ```

To keep Zubi running after a restart, switch on **Launch at login** under **Settings › General**.

## Use it from another device

At first Zubi only answers on your Mac, so nothing on your network can reach it by accident. To read it
from another device, either:

- **Tunnel to it** (the safest). Turn on Remote Login on the Mac, then on the other device run
  `ssh -N -L 8787:localhost:8787 you@mac.local`. Zubi is now at `http://127.0.0.1:8787` there too.
- **Use a private network** such as Tailscale. Under **Settings › Server**, choose **Specific IP**,
  enter the Mac's Tailscale address, and click **Apply and Restart**.

The Mac has to be awake, logged in and running Zubi to answer.
[Reaching Zubi from other devices](https://fa-krug.github.io/zubi/docs.html#network) covers every
option.

## The `zubi` command

`zubi` is a small command-line client for macOS, Linux (including a Raspberry Pi) and Windows. It shows,
searches, adds and edits your reminders, events and notes in a terminal, gives scripts and AI agents
exact JSON with `--json`, prints a line whenever something changes (`zubi watch`), and serves
Zubi to AI assistants such as Claude Desktop (`zubi mcp`).

```bash
zubi config discover --save        # or: zubi config set url http://127.0.0.1:8787
zubi config set key -              # paste a key from Settings › Keys
zubi events --from 2026-11-02 --to 2026-11-06
zubi reminders add "Buy milk" --list Groceries --due 2026-11-02
zubi reminders --due overdue --json
```

Install it with Homebrew on macOS and Linux, or Scoop on Windows:

```bash
brew install fa-krug/tap/zubi
```

```powershell
scoop bucket add fa-krug https://github.com/fa-krug/scoop-bucket
scoop install fa-krug/zubi
```

Or download it from the [CLI releases](https://github.com/fa-krug/zubi/releases?q=cli&expanded=true)
(tagged `cli/v…`). [cli/README.md](cli/README.md) has every install option, command and exit code.

## Privacy and security

- You decide per list, calendar and folder: **Off**, **Read**, or **Read & Write**. Anything Off
  answers as if it didn't exist; anything set to Read can't be changed through Zubi.
- Every device gets its own key, kept in your Mac's Keychain: **Read**, or **Read & Write**. A Read
  key can never change anything, and you can revoke one device without touching the others.
- **Settings › Activity** lists everything created or edited through Zubi, and by which key.
- Zubi listens only on your Mac until you change that. It only adds and edits where you chose Read &
  Write, and it never deletes anything.
- Zubi doesn't encrypt traffic itself. Beyond your Mac, use an SSH tunnel or an encrypted network such
  as Tailscale.
- No analytics, no tracking. The only outside service Zubi contacts is GitHub, once a day, to check for
  updates; you can switch that off.

The [privacy policy](https://fa-krug.github.io/zubi/privacy.html) lists every connection Zubi makes.

## Documentation

- [Documentation](https://fa-krug.github.io/zubi/docs.html): the app, every setting, what each source
  returns, other devices, updates and troubleshooting.
- [API reference](https://fa-krug.github.io/zubi/api.html): every endpoint, parameter and field. On your
  Mac, **Open API Docs** in **Settings › Keys** opens the same API in a page where you can try each call.
- [Command-line client](cli/README.md): install, configure, commands, and use from agents.

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
