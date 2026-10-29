# Zubi

*Zubi* is Basque for "bridge": between your Mac and everything else.

<p align="center">
  <img alt="macOS 14 or later"
       src="https://img.shields.io/badge/macOS-14%2B-000000?style=flat&logo=apple&logoColor=white">
  <a href="LICENSE">
    <img alt="License: MIT"
         src="https://img.shields.io/badge/License-MIT-3DA639?style=flat"></a>
</p>

Zubi is a free Mac app that shares the reminders, calendars and notes you choose with your other
devices: a Raspberry Pi, a Linux or Windows computer, an AI assistant. From anywhere, encrypted end
to end, with no account.

**Documentation: <https://zubi.fa-krug.de/docs.html>**

## Get started

1. **On your Mac:** download Zubi from [Releases](https://github.com/fa-krug/zubi/releases/latest),
   move it to Applications and open it. It connects on its own and starts with your Mac from now on.
   (macOS 14 or later.)
2. **Choose what to share:** in Zubi's Settings, under Reminders, Calendars and Notes, click
   **Grant Access** and pick what your devices may see (**Read**) or also change (**Read & Write**).
3. **On your device:** install the client and run it, then paste the code from Zubi's menu bar icon ›
   **Copy Pairing Code**.

   ```bash
   curl -fsSL https://zubi.fa-krug.de/install.sh | sh
   zubi
   ```

That's it. Now try:

```bash
zubi reminders
zubi events
zubi reminders add "Buy milk" --list Groceries
```

`zubi help` lists every command. For AI assistants, `zubi mcp`: in Claude Code,
`claude mcp add zubi -- zubi mcp`. On Windows, or with Homebrew, see [cli/README.md](cli/README.md).

## Privacy

Nothing is shared until you choose it, and Zubi never deletes anything. Everything between your Mac
and a device is encrypted with that device's code; the relay in between only passes it on. Zubi's own
relay is the default; you can also [run your own](https://zubi.fa-krug.de/docs.html#relay), at home
or on a server. The
[privacy policy](https://zubi.fa-krug.de/privacy.html) has the details.

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
