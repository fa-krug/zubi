# zubi

`zubi` is the client for [Zubi](../README.md). It reads and changes the reminders, calendars and notes
your Mac shares, from Linux, a Raspberry Pi, macOS or Windows, wherever the Mac is.

## Get started

```sh
curl -fsSL https://zubi.fa-krug.de/install.sh | sh
zubi
```

`zubi` asks for a pairing code: on your Mac, click Zubi's menu bar icon › **Copy Pairing Code**, and
paste it. That's all. On Linux it also sets up a small background service that keeps the connection
(it asks for `sudo` once).

Afterwards, `zubi` on its own shows whether everything works:

```text
$ zubi
✓ Paired as "Default", through wss://relay.fa-krug.de
✓ The zubi-client service keeps the connection
✓ Your Mac answers
```

If a line has a ✗, it says what to do. Treat the pairing code like a password.

**Other ways to install:** `brew install fa-krug/tap/zubi` on macOS and Linux; on Windows,
[Scoop](https://scoop.sh): `scoop bucket add fa-krug https://github.com/fa-krug/scoop-bucket`, then
`scoop install fa-krug/zubi`. Or download a build from the
[releases](https://github.com/fa-krug/zubi/releases?q=cli&expanded=true) (tagged `cli/v…`). To update,
run the same install command again.

## Commands

```sh
zubi reminders                     # open reminders
zubi reminders --list Groceries
zubi reminders --due today         # or: overdue
zubi events                        # this week
zubi events --from 2026-11-02 --to 2026-11-06
zubi notes                         # newest first
zubi note ID                       # one note's text
zubi lists                         # what is shared; also: calendars, folders

zubi reminders add "Buy milk" --list Groceries --due 2026-11-02
zubi reminders done ID
zubi events add Dentist --calendar Personal --start 2026-11-03T09:00 --end 2026-11-03T09:30
zubi notes add Pancakes --folder Recipes --text "Flour, eggs, milk"

zubi watch                         # a line whenever something changes
zubi help                          # everything else
```

- **IDs** for `done`, `edit` and `note` come from `--ids` on `reminders` and `events`, and from
  `zubi notes`.
- **Dates** are `YYYY-MM-DD`, or `YYYY-MM-DDTHH:MM` in this computer's time.
- **Adding and editing** only works where the list, calendar or folder is set to **Read & Write** on
  the Mac. Zubi never deletes anything.
- `zubi watch --exec ./refresh.sh` runs a command after every change, for a display.

## AI assistants

`zubi mcp` gives Claude and other MCP clients your reminders, calendars and notes.

- Claude Code: `claude mcp add zubi -- zubi mcp`
- Claude Desktop: add this to `claude_desktop_config.json` (Settings › Developer › Edit Config):

  ```json
  { "mcpServers": { "zubi": { "command": "/usr/local/bin/zubi", "args": ["mcp"] } } }
  ```

## Scripts

Add `--json` for exact data on stdout; errors go to stderr. The exit code says what happened:

| Code | Means |
| ---- | ----- |
| 0 | Worked |
| 1 | Refused, such as a change to something shared read-only |
| 2 | Wrong command or flag, or not paired yet |
| 3 | The Mac refused this computer's code: pair again with `zubi pair -` |
| 4 | Not found, or not shared |
| 5 | The Mac doesn't answer: asleep, or Zubi not running |

```sh
zubi reminders --due overdue --json | jq -r '.reminders[].title'
```

## Pairing again

`zubi pair -` pairs with a new code (paste it, then Enter). `zubi unpair` forgets the pairing and
removes the background service.

## Your own relay

You don't need this: Zubi connects through its own relay, `relay.fa-krug.de`. If you'd rather run
your own, pick one of these, on a machine that is always on, such as a Raspberry Pi.

**With a Cloudflare Tunnel** (no router changes; needs a domain on Cloudflare):

```sh
zubi relay --behind-tls-proxy --listen 127.0.0.1:8080 --install-service
```

In the Cloudflare dashboard, give your tunnel a public hostname such as `relay.example.com`, type
**HTTP**, URL `127.0.0.1:8080` (not `localhost`). `https://relay.example.com/health` then answers `ok`,
and the relay's address is `wss://relay.example.com`.

**With a port forwarded:** forward TCP port 443 on your router to port 8443 on the machine, then:

```sh
zubi relay --listen :8443 --install-service
journalctl -u zubi-relay      # shows the relay's address
```

It gets and renews its certificate by itself. This needs a public IPv4 address from your internet
provider.

Then, on your Mac, in Zubi's Settings › Connection, turn on **Use my own relay**, enter the address,
click **Apply**, and pair your devices again.

`--install-service` keeps the relay running and starts it at boot (Linux). `systemctl status
zubi-relay` shows whether it runs; `zubi relay --uninstall-service` removes it. `zubi help` lists
the other options (a domain instead of an IP address, another port, plain HTTP behind any reverse
proxy).

## Development

See [CONTRIBUTING.md](../CONTRIBUTING.md#work-on-the-cli) and, for the relay's protocol,
[docs/features/relay.md](../docs/features/relay.md).
