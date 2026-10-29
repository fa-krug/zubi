# zubi

`zubi` is the client for [Zubi](../README.md): it reads and changes the Reminders, calendars and
Notes your Mac shares, from Linux (including a Raspberry Pi), macOS or Windows, wherever the Mac is.
It reaches the Mac through a relay, `wss://relay.fa-krug.de` unless you run your own, and each
connection is encrypted end to end between this computer and the Mac with the pairing code's key: the
relay passes along only what it can't read.

```sh
curl -fsSL https://zubi.fa-krug.de/install.sh | sh
zubi
```

## Getting started

Run `zubi`. The first time, it asks for a pairing code: on the Mac, copy one from **Zubi › Settings ›
Connection**, or add a client for this computer under **Settings › Clients** and copy its code, then
paste it. `zubi` checks it with the Mac through the relay and saves it only when that works, so a typo
or a sleeping Mac shows at once. The code holds this computer's key: treat it as a password.

On Linux, it then sets up the `zubi-client` service (asking for `sudo`), which keeps the connection to
the Mac, starts at boot, and lets every command start without connecting again. Elsewhere commands
connect each time, which works the same, only a moment slower.

From then on, `zubi` on its own shows the connection's status:

```text
$ zubi
✓ Paired as "Kitchen Pi", through wss://relay.fa-krug.de
✓ The zubi-client service keeps the connection
✓ Your Mac answers

Try `zubi reminders`, `zubi events` or `zubi notes`; `zubi help` lists everything.
```

A ✗ says what is wrong: the service isn't running (`systemctl status zubi-client` says why), the Mac
refused the code (it was removed or renewed under Settings › Clients: `zubi pair -` with a new one),
or the Mac doesn't answer (asleep, Zubi not running, or not connected to the relay; its menu bar icon
shows which).

| Command | Does |
| --- | --- |
| `zubi` | The status; pairs the first time |
| `zubi pair CODE` | Pair again, with a new code or for another relay. `zubi pair -` reads the code from stdin, so it stays out of your shell history |
| `zubi unpair` | Forget the pairing and remove the `zubi-client` service |

## What it can see and change

Each reminder list, calendar and Notes folder is set in Zubi's Settings to one of:

| Setting          | `zubi` can                                    | Otherwise                         |
| ---------------- | --------------------------------------------- | --------------------------------- |
| **Off**          | nothing; it is invisible                      | 404, exit code 4                  |
| **Read**         | list and read it                              | adding or editing answers 403, exit code 1 |
| **Read & Write** | also add items and edit them                  |                                   |

`zubi lists`, `zubi calendars` and `zubi folders` mark what is shared read-only (`read-only` after
the name; `"writable": false` in `--json`). A source can be read-only on its own account too: a
subscribed or holiday calendar never takes changes, whatever the setting.

Nothing is ever deleted through Zubi. You can add, rename, move, reschedule and complete, and clear
a field (`--clear-due`), but removing a reminder, event or note is done on the Mac. Zubi never
changes a locked note.

## Install

### Install script (Linux, Raspberry Pi, macOS)

```sh
curl -fsSL https://zubi.fa-krug.de/install.sh | sh
```

The quickest way on a Raspberry Pi, and the only one-line install for 32-bit Raspberry Pi OS. It
picks the build for the machine (Linux on x86-64, 64-bit ARM or 32-bit ARMv7, or macOS), downloads
it from the newest CLI release, checks it against the release's SHA-256 checksums, and installs
`zubi` in `/usr/local/bin`, asking for `sudo` only if that needs it. Run it again to update; it says
so when `zubi` is already current.

- `ZUBI_VERSION=0.1.42` installs that version instead of the newest.
- `ZUBI_INSTALL_DIR=$HOME/.local/bin` installs there, without `sudo`.

```sh
curl -fsSL https://zubi.fa-krug.de/install.sh | ZUBI_INSTALL_DIR=$HOME/.local/bin sh
```

It needs `curl` or `wget`, and `tar`. To read it before running it, download it first:
`curl -fsSLO https://zubi.fa-krug.de/install.sh`, then `sh install.sh`. The Raspberry Pi Zero
and Pi 1 (ARMv6) have no build.

### Homebrew (macOS and Linux)

```sh
brew install fa-krug/tap/zubi
```

`brew upgrade` keeps it current. Homebrew on Linux covers x86-64 and ARM64; for 32-bit Raspberry Pi
OS, use the install script above or the ARMv7 archive below.

### Scoop (Windows)

```powershell
scoop bucket add fa-krug https://github.com/fa-krug/scoop-bucket
scoop install fa-krug/zubi
```

### From a release

Releases are tagged `cli/vX.Y.Z` at <https://github.com/fa-krug/zubi/releases>. Each has one
archive per platform, named `zubi_<version>_<os>_<arch>`:

| Platform                | Archive                                |
| ----------------------- | -------------------------------------- |
| macOS (Intel and Apple) | `zubi_<version>_darwin_universal.tar.gz` |
| Linux x86-64            | `zubi_<version>_linux_amd64.tar.gz`    |
| Linux ARM64             | `zubi_<version>_linux_arm64.tar.gz`    |
| Linux ARMv7             | `zubi_<version>_linux_armv7.tar.gz`    |
| Windows x86-64          | `zubi_<version>_windows_amd64.zip`     |
| Windows ARM64           | `zubi_<version>_windows_arm64.zip`     |

Each archive holds a folder of the same name with the binary (`zubi`, or `zubi.exe` on Windows),
this README and the licences; `zubi_<version>_checksums.txt` lists their SHA-256 sums. The macOS
binary is signed and notarized. Put the binary on your `PATH`:

```sh
tar -xzf zubi_0.1.42_linux_arm64.tar.gz
sudo install zubi_0.1.42_linux_arm64/zubi /usr/local/bin/
```

### With Go

```sh
go install github.com/fa-krug/zubi/cli/cmd/zubi@latest
```

This needs Go 1.26 or later and puts `zubi` in `$(go env GOPATH)/bin`. A build from source
reports its version as `dev`.

The settings file, with the pairing code, is JSON readable only by its owner:

| OS      | Path                                                  |
| ------- | ----------------------------------------------------- |
| macOS   | `~/Library/Application Support/zubi/config.json`     |
| Linux   | `$XDG_CONFIG_HOME/zubi/config.json`, else `~/.config/zubi/config.json` |
| Windows | `%AppData%\zubi\config.json`                          |

`ZUBI_CONFIG` points `zubi` at another file. The `zubi-client` service listens on `zubi.sock` in the
same folder, which only the owner can open.

## Commands

### Reading

| Command      | Shows                                  | Flags |
| ------------ | -------------------------------------- | ----- |
| `lists`      | Reminder lists; read-only ones marked  | |
| `reminders`  | Reminders, incomplete unless asked     | `--list NAME\|ID`, `--due today\|overdue`, `--completed`, `--search TEXT`, `--ids` (each reminder's ID below it) |
| `calendars`  | Calendars; read-only ones marked       | |
| `events`     | Events, grouped by day; recurring events appear once per occurrence | `--from YYYY-MM-DD`, `--to YYYY-MM-DD` (inclusive; default today to six days on), `--calendar NAME\|ID`, `--search TEXT`, `--ids` (each event's ID and `--occurrence` below it) |
| `folders`    | Notes folders, as a tree; read-only ones marked | |
| `notes`      | Note titles and IDs, newest first      | `--folder NAME\|ID`, `--limit N` (1–500, default 50), `--search TEXT` |
| `note ID`    | One note's text                        | `--html` for the note's HTML |
| `version`    | The client's version                   | |

`--search TEXT`, or `-q TEXT`, keeps the items whose title or notes contain `TEXT`, ignoring
case. For notes it searches their text, which reads every note in the folder and can take a few
seconds in a large one.

### Changing

Only in lists, calendars and folders set to **Read & Write** (see above). Each command prints the
item as the listing shows it, followed by its ID (and, for an event, its `--occurrence`), so you
can edit it next.

| Command | Required | Optional |
| ------- | -------- | -------- |
| `reminders add TITLE` | `--list NAME\|ID` | `--due WHEN`, `--notes TEXT`, `--priority P` |
| `reminders edit ID` | at least one change | `--title TEXT`, `--list NAME\|ID` (moves it), `--due WHEN` or `--clear-due`, `--notes TEXT` or `--clear-notes`, `--priority P`, `--done` or `--undone` |
| `reminders done ID` | | Same as `reminders edit ID --done` |
| `events add TITLE` | `--calendar NAME\|ID`, `--start WHEN`, `--end WHEN` | `--all-day` (then `--start` and `--end` are days, and `--end` defaults to `--start`), `--location TEXT`, `--notes TEXT`, `--link URL` |
| `events edit ID` | at least one change | `--occurrence START`, `--title TEXT`, `--start WHEN`, `--end WHEN`, `--all-day` or `--timed`, `--calendar NAME\|ID` (moves it), `--location TEXT` or `--clear-location`, `--notes TEXT` or `--clear-notes`, `--link URL` or `--clear-link` |
| `notes add TITLE` | `--folder NAME\|ID` | `--text TEXT` or `--html HTML` |
| `note edit ID` | at least one change | `--title TEXT`, `--text TEXT` or `--html HTML` (replaces the whole body) |

- **An edit changes only what you pass.** A flag you leave out keeps its value; `--clear-…` empties
  the field.
- **`TITLE` is one argument**: quote it when it has spaces.
- **`WHEN`** is `YYYY-MM-DD` for a day (an all-day reminder, or a day of an `--all-day` event),
  `YYYY-MM-DDTHH:MM` for a time on this machine's clock, or ISO 8601 with a zone
  (`2026-11-02T09:00:00Z`, `2026-11-02T10:00+01:00`). A timed event takes times, an all-day one
  days.
- **`P`**, the priority, is `none`, `high`, `medium` or `low`, or EventKit's 0–9 (0 none, 1–4
  high, 5 medium, 6–9 low).
- **`--text -`** and **`--html -`** read the body from stdin.
- **A recurring event** shares one ID across its occurrences. `--occurrence` picks the one to
  change: its start, exactly as `zubi events --ids` (or `.start` in `--json`) shows it. Only that
  occurrence changes. A recurring event without `--occurrence` answers 400.

Flags every command accepts, anywhere on the line:

| Flag            | Meaning |
| --------------- | ------- |
| `--json`        | Print Zubi's JSON response instead of text |
| `--timeout DUR` | Give up after this long, such as `5s` or `1m` (default `20s`; Notes can be slow) |
| `--no-color`    | No colour |

Text output is coloured only on a terminal. `NO_COLOR` or `--no-color` turns colour off;
`CLICOLOR_FORCE=1` turns it on for a pipe, such as `zubi events | less -R`.

Note IDs are `x-coredata://…` URLs. Copy them from `zubi notes`; `zubi` encodes them for you.

```sh
zubi reminders --list Groceries
zubi reminders -q milk --completed
zubi events --from 2026-11-02 --to 2026-11-06 --calendar Work
zubi notes --folder Recipes --limit 10
zubi note 'x-coredata://…/ICNote/p123'

zubi reminders add "Buy milk" --list Groceries --due 2026-11-02 --priority high
zubi reminders --ids                         # find the ID
zubi reminders done 4F1C…                    # complete it
zubi reminders edit 4F1C… --due 2026-11-03T18:00 --clear-notes
zubi events add Dentist --calendar Personal --start 2026-11-03T09:00 --end 2026-11-03T09:30
zubi events add Offsite --calendar Work --start 2026-11-09 --end 2026-11-10 --all-day
zubi events edit 9A2E…:7B1D… --occurrence 2026-11-02T09:00:00Z --start 2026-11-02T09:30 --end 2026-11-02T09:45
zubi notes add Pancakes --folder Recipes --text - < pancakes.txt
zubi note edit 'x-coredata://…/ICNote/p123' --title "Pancakes (v2)"
```

### Following changes

`zubi watch` keeps a connection to Zubi open and prints a line whenever something changes, until
you press Ctrl-C:

```
14:03:12  reminders changed (elsewhere)
14:05:40  calendar changed (through Zubi)
```

The line says what changed (`reminders`, `calendar` or `notes`) and whether the change came
through Zubi or from elsewhere: the Mac, iCloud or another device. Zubi doesn't say which
item changed; re-read what you show. Notes has no change notifications on the Mac, so only
changes made through Zubi are reported for it.

- **`--json`** prints each change as one line of JSON (`{"source":"reminders","origin":"elsewhere"}`),
  for scripts.
- **`--exec COMMAND`** runs `COMMAND` after each change, such as `--exec "./refresh-dashboard.sh"`.
  The command line is split into words at spaces (quote a word with spaces in `'…'` or `"…"`) and
  run directly, without a shell, so nothing Zubi sends can end up in it. The command finds the
  change in `ZUBI_CHANGE_SOURCE` and `ZUBI_CHANGE_ORIGIN`. One run happens at a time; changes that
  arrive during a run cause one more run after it. Its output goes to stderr, so stdout stays the
  change lines. For pipes or variables, run a shell yourself: `--exec "sh -c '…'"`.
- When the connection drops (Zubi restarts, the Mac sleeps, the network or the relay goes), `zubi watch`
  reconnects, waiting a little longer after each failure up to 30 seconds. Zubi sends a
  keep-alive every 30 seconds, so a connection that stays silent for longer is treated as dropped.
  Changes made while it was disconnected are not reported. A refused pairing (exit 3) or a Zubi too
  old to report changes (exit 4) ends it.
- `--timeout` limits connecting, not how long it watches. Ctrl-C exits 0.

### Using it with Claude Desktop and other MCP clients

`zubi mcp` serves Zubi to AI assistants over the [Model Context
Protocol](https://modelcontextprotocol.io): the client starts it, and talks to it over stdin and
stdout. It uses this computer's pairing, so pair first, as the same user the assistant runs as. To
tell the assistant's changes apart in Settings › Activity, pair that computer with a client of its
own.

For Claude Desktop, add it to `claude_desktop_config.json` (**Settings › Developer › Edit
Config**), with the full path to `zubi`:

```json
{
  "mcpServers": {
    "zubi": {
      "command": "/usr/local/bin/zubi",
      "args": ["mcp"]
    }
  }
}
```

For Claude Code:

```sh
claude mcp add zubi -- zubi mcp
```

Other clients take the same command, `zubi mcp`.

The tools are `list_reminder_lists`, `list_reminders`, `list_calendars`, `list_events`,
`list_note_folders`, `list_notes`, `get_note`, `create_reminder`, `update_reminder`,
`create_event`, `update_event`, `create_note`, `update_note` and `get_key`. They answer with Zubi's
JSON, and with an explanation the assistant can act on when Zubi refuses (a read-only list, a locked
note). The assistant sees the same lists, calendars and folders as `zubi`, and can't delete anything
either. `zubi mcp` logs to stderr, which clients keep in their MCP logs; it starts without a pairing,
and then each tool answers that one is needed.

## Using it from an agent

- **Always pass `--json`.** Text output is for people and may change; with `--json`, stdout is Zubi's
  response body, indented, and nothing else. Fields without a value are left out, not `null`.
- **Branch on the exit code**, not on the text:

  | Code | Meaning |
  | ---- | ------- |
  | 0    | Success; stdout holds the result |
  | 1    | Any other error: a read-only list, calendar or folder, or access not granted on the Mac (403); a locked note (409); Notes unreadable (503); a request Zubi refused, such as a bad date range (400) |
  | 2    | Bad usage: unknown command or flag, invalid value, missing title or required flag, an edit with nothing to change, conflicting flags (`--due` with `--clear-due`, `--text` with `--html`), or not paired yet. Nothing was sent. |
  | 3    | The Mac refused this computer's pairing: its client was removed or given a new code |
  | 4    | Not found or not shared (404): an unknown list, calendar, folder, reminder, event or note, or one set to Off in Zubi's Settings |
  | 5    | The Mac doesn't answer: asleep, Zubi not running, or the relay unreachable |

- **On failure, stdout is empty and stderr carries one line** starting `zubi:` that says what went
  wrong, including Zubi's message when it sent one.
- `zubi watch --json` prints one JSON object per line as things change, and runs until it is
  killed; read it line by line.

### Changing things from an agent

- **IDs come from the listings**: `.reminders[].id` from `zubi reminders --json`, `.events[].id`
  and `.start` from `zubi events --json`, `.notes[].id` from `zubi notes --json`. Lists, calendars
  and folders can be named by title or ID.
- **Check `writable` first.** `zubi lists --json`, `zubi calendars --json` and `zubi folders --json`
  say for each one whether Zubi may change it. Writing to one that isn't answers 403 (exit 1);
  retrying won't help. Tell the user to set it to Read & Write in Zubi's Settings on the Mac.
- **A write prints the item as the server now has it** (`--json`: the API's Reminder, Event or
  Note), including its `id`, so you can follow up without listing again. Creating answers 201,
  editing 200; both exit 0.
- **Edits are partial**: send only the flags for what should change. `--clear-due`,
  `--clear-notes`, `--clear-location` and `--clear-link` send `null` and empty the field.
- **Pass times with a zone** (`2026-11-02T09:00:00Z`) or as a day (`2026-11-02`), so the result
  doesn't depend on the clock of the machine running `zubi`.
- **For a recurring event, pass `--occurrence`** with the `start` you listed; without it the
  edit answers 400. Only that occurrence changes.
- Writes are not idempotent: if an `add` failed with exit 5 or a timeout, list before retrying, in
  case the first one landed.
- Nothing can be deleted. To "remove" a reminder, complete it.

```sh
# Titles of overdue reminders
zubi reminders --due overdue --json | jq -r '.reminders[].title'

# Today's timed events as "HH:MM title" (times are ISO 8601 in UTC)
zubi events --to "$(date +%F)" --json |
  jq -r '.events[] | select(.isAllDay | not) | "\(.start[11:16]) \(.title)"'

# The newest note in a folder, as plain text
id=$(zubi notes --folder Recipes --limit 1 --json | jq -r '.notes[0].id')
zubi note "$id" --json | jq -r '.plaintext // "(locked)"'

# Add a reminder and complete it later, by the ID the add printed
id=$(zubi reminders add "Renew passport" --list Errands --due 2026-12-01 --json | jq -r .id)
zubi reminders done "$id" --json > /dev/null

# Lists the agent may write to
zubi lists --json | jq -r '.lists[] | select(.writable) | .title'

# Move the standup on 3 November by half an hour
ev=$(zubi events --from 2026-11-03 --to 2026-11-03 --json |
  jq -c '.events[] | select(.title == "Standup")')
zubi events edit "$(jq -r .id <<<"$ev")" --occurrence "$(jq -r .start <<<"$ev")" \
  --start 2026-11-03T09:30:00Z --end 2026-11-03T09:45:00Z --json

# A new note from a file
zubi notes add "Trip checklist" --folder Travel --text - --json < checklist.txt | jq -r .id

# Act on the exit code
zubi lists --json > lists.json
case $? in
  0) ;;
  3) echo "the Mac refused this pairing" >&2 ;;
  5) echo "the Mac does not answer" >&2 ;;
  *) echo "failed" >&2 ;;
esac
```

## Your own relay

Zubi uses `wss://relay.fa-krug.de` unless you set your own. Run one with `zubi relay` on a machine that
is always on; a Raspberry Pi works well, and can be the device that reads your reminders at the same
time. Then:

1. On the Mac, under **Zubi › Settings › Connection**, turn on **Use my own relay**, enter its address,
   such as `wss://relay.example.com`, and click **Apply**. The status turns green once the Mac is
   connected to it.
2. On each device, pair again with a code from **Settings › Clients**: `zubi pair -`. A pairing code
   names its relay, so the old codes still point at the old one.

The relay only copies messages between the Mac and its devices; it can't read them, and sees only
which addresses connect, when, and how much they send. What decides the setup is where its `wss://`
address comes from; pick the first that fits.


The relay is `zubi relay`, on a machine that is always on. A Raspberry Pi works well, and can be the
device that reads your reminders at the same time. What decides the setup is where its `wss://`
address comes from; pick the first that fits.

### Behind a Cloudflare Tunnel

No port forwarding and no certificate to manage; you need a domain on Cloudflare and `cloudflared`
running on the machine.

1. Run the relay for the tunnel, as a service that starts at boot (see
   [Keeping it running](#keeping-it-running)):

   ```sh
   zubi relay --behind-tls-proxy --listen 127.0.0.1:8080 --install-service
   ```

2. In the Cloudflare dashboard, open your tunnel and add a **Public Hostname**, such as
   `relay.example.com`, with service type **HTTP** and URL `127.0.0.1:8080`. Write `127.0.0.1`, not
   `localhost`: on the Pi, `localhost` can mean the IPv6 address `::1`, where another program may
   answer. In a `cloudflared` config file, that is:

   ```yaml
   ingress:
     - hostname: relay.example.com
       service: http://127.0.0.1:8080
     - service: http_status:404
   ```

3. Check it from anywhere:

   ```sh
   curl https://relay.example.com/health    # answers: ok
   ```

   `404 page not found` means the tunnel reaches some other program: on the machine running
   `cloudflared`, `curl http://127.0.0.1:8080/` answers `Zubi relay. …` when the relay listens there,
   and `sudo ss -ltnp | grep 8080` shows what listens on that port. A Cloudflare error page (502, 530)
   means nothing answers behind the tunnel at all.

The relay's address is `wss://relay.example.com`. Cloudflare passes WebSockets on and tells the
relay the connection arrived over TLS, which `--behind-tls-proxy` insists on. A quick tunnel
(`trycloudflare.com`) gets a new name on every start, so use a named tunnel. Any other reverse proxy
that serves `wss://` works the same way, as long as it passes WebSocket upgrades on and sets
`X-Forwarded-Proto` (Caddy does).

### On a machine at home, with a port forwarded

Free, and nobody else involved. Forward TCP port 443 on your router to the machine's port 8443, then:

```sh
zubi relay --listen :8443 --install-service
journalctl -u zubi-relay     # shows "Relay ready: wss://203.0.113.7", the relay's address
```

It finds your network's public IPv4 address, gets a certificate for it from Let's Encrypt and renews
it (six days at a time) on its own. Let's Encrypt checks by connecting to port 443 of that address,
so it must reach the machine from the internet; when it can't, the relay says why and exits rather
than falling back to unencrypted `ws://`. Two things to check:

- Your connection needs its own public IPv4 address, not carrier-grade NAT (common on mobile and some
  fibre lines). If Let's Encrypt can't connect even with the port forwarded, ask your provider.
- When your provider changes that address, the relay notices within an hour and gets a new
  certificate, but the Mac still has the old address. A free [DuckDNS](https://www.duckdns.org)
  name with `--domain` avoids that.

### On Render

With no machine at home, Render's free web service runs the relay and serves `wss://` with its own
certificate. New › Web Service › Public Git Repository `https://github.com/fa-krug/zubi`, then:

| Setting | Value |
| --- | --- |
| Language | Go |
| Root Directory | `cli` |
| Build Command | `go build -o zubi ./cmd/zubi` |
| Start Command | `./zubi relay --behind-tls-proxy` |
| Instance Type | Free |
| Health Check Path | `/health` (under Advanced) |

The relay's address is the service's URL with `wss://`, such as `wss://zubi-relay.onrender.com`.
Free services sleep after 15 minutes without traffic; a connected Mac pings every 30 seconds, which
keeps it awake, and 750 free hours a month cover one service running all month. If it does sleep,
the first connection takes about a minute. Deploying again picks up a newer `zubi`.

### On a server with a public address

Any VPS, or Oracle Cloud's Always Free tier: `zubi relay --install-service`, as at home but without
the router, and the address never changes. Oracle may reclaim an Always Free machine that stays idle
for a week, and a relay is mostly idle.

Not a good fit: Google Cloud Run cuts every WebSocket after at most an hour, and its free tier needs a
billing account that a connection open around the clock would soon draw on.

### Keeping it running

`--install-service` turns the command it is added to into a systemd service (Linux), so the relay
keeps running after you log out or close the SSH session, starts at boot, and restarts after a
failure. It records the other flags as they are, asks for `sudo` to write
`/etc/systemd/system/zubi-relay.service`, and runs the relay as you, so its certificate stays in your
config directory. Stop a relay still running in a terminal first.

```sh
systemctl status zubi-relay          # whether it runs
journalctl -u zubi-relay -f          # its log, as it runs
zubi relay --uninstall-service       # stop and remove it
```

To change its flags, run `zubi relay` with the new flags and `--install-service` again. On port 443
the service gets the one privilege that takes; after a failed certificate it waits 15 minutes before
trying again, as Let's Encrypt allows five failed checks per address an hour.

### Flags

| Flag | Does |
| --- | --- |
| `--listen ADDR` | Listen here instead of `:443`. Let's Encrypt still connects to port 443, so forward that to this port. |
| `--ip IP` | Certify this address instead of the one found automatically (IPv6 works too). |
| `--domain NAME` | Certify a domain name pointing at this machine, such as a free DuckDNS name, so the address survives a new IP from your provider. |
| `--state DIR` | Where the certificate and Let's Encrypt account key are kept (default `~/.config/zubi/relay`, readable only by you). |
| `--acme-directory URL` | Another ACME certificate authority, such as Let's Encrypt's staging one for testing. |
| `--behind-tls-proxy` | Serve plain HTTP on `$PORT` (else `:8080`) for a tunnel, platform or reverse proxy that serves `wss://` itself. WebSockets the proxy didn't receive over TLS are refused. |
| `--no-tls` | Serve plain `ws://` on `$PORT` (else `:8080`). Only on a network you trust: the content stays end-to-end encrypted, but the metadata doesn't. Zubi and `zubi` don't use a `ws://` relay. |
| `--install-service`, `--uninstall-service` | Set up or remove the systemd service, as above. |

On start the relay prints its address, or behind a proxy what to point the proxy at, and the next
steps. It stops on Ctrl-C or SIGTERM, exits with 1 when it can't start (no certificate, port taken)
and with 2 on bad flags. The log goes to stderr: a line when a Mac connects or leaves, with the start
of its room ID, and certificate renewals. It logs no client addresses.

## Development

The client is a Go module in `cli/` of the Zubi repository. It uses only the standard library, except
for the relay's certificates: `internal/relay` uses `golang.org/x/crypto/acme`.

```sh
cd cli
go test ./...
go build -ldflags "-X main.version=1.2.3" ./cmd/zubi
```

Every request the client can make is one entry in `apiCommands` (`cmd/zubi/commands.go`): its method,
path, query parameters, request body type, response type and media type. The protocol inside a session
is HTTP, described by `Packages/ZubiAPI/Sources/ZubiAPI/Resources/openapi.json`; the tests read it and
fail when an entry, a path or query parameter, a request body struct (field names, required vs.
optional, which fields may be `null`) or a response struct disagrees with it, or when it gains an
operation or a query parameter no command sends. They also validate the fixtures in
`cmd/zubi/testdata/` against it, and run every command against a fake Zubi that checks each request's
method, path, parameters, body and `Accept` header against it and answers with those fixtures.

The MCP tools (`mcpTools` in `cmd/zubi/mcp.go`) each name an entry in `apiCommands` and send
through the same client. The tests check that each tool's arguments are exactly its operation's
parameters and body properties (names, types, enums, bounds, required), that every operation an
assistant could use has a tool, and drive every tool through `zubi mcp` against the fake.

The relay (`internal/relay`, `internal/websocket`) has its own tests: WebSocket framing, the relay
protocol from registration to a closed session, limits and STUN. Sessions through it
(`internal/noise`, `internal/tunnel`) are tested against vectors from an independent Noise
implementation, shared with the app's tests, and end to end: pairing, `zubi serve`, commands and `zubi watch` run against the fake Zubi through a real relay and a Go stand-in for the Mac
(`tunnel.Host`). Its certificate tests need Pebble,
Let's Encrypt's test CA, and are skipped without it:
`ZUBI_PEBBLE=$(go env GOPATH)/bin/pebble go test ./internal/relay/` after
`go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1`. The protocol and the relay's rules
are in [docs/features/relay.md](../docs/features/relay.md).
