# zubi CLI

`zubi` reads and changes the Reminders, calendars and Notes that a Mac running
[Zubi](../README.md) shares over its HTTP API. It runs on macOS, Linux and Windows, needs nothing
on the Mac beyond Zubi itself, and prints either readable text or the API's JSON. It can also
follow changes as they happen, hand calendar apps a subscription URL, serve Zubi to AI
assistants over the Model Context Protocol, and run a relay for a Mac and clients on different
networks.

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

### Keys

Zubi gives each device its own key, made under **Settings › Keys**, with one of two kinds of
access:

- **Read** keys can only read, whatever the lists, calendars and folders allow. Every add or edit
  answers 403.
- **Read & Write** keys can also add and edit, where the list, calendar or folder is set to
  Read & Write too.

`zubi key` shows the name and access of the key you are using; `zubi health` mentions it as well.
Give a script, a calendar app or an AI assistant a key of its own, read-only unless it needs to
write, so you can take it back without touching the others.

## Install

### Homebrew (macOS and Linux)

```sh
brew install fa-krug/tap/zubi
```

`brew upgrade` keeps it current. Homebrew on Linux covers x86-64 and ARM64; for 32-bit Raspberry Pi
OS, use the ARMv7 archive below.

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

## Configure

`zubi` needs two things: where Zubi is, and a key made in Zubi under **Settings › Keys**.

```sh
zubi config discover --save  # finds Zubi on this network and stores its URL
zubi config set key -        # paste the key, then Enter; it stays out of your shell history
zubi config                  # shows the file, the URL and the key (masked)
zubi health                  # checks that Zubi answers, and names the key and its access
```

`zubi config set url http://mac.local:8787` sets the URL by hand. `zubi config unset url` and
`zubi config unset key` remove a setting.

### Finding Zubi on the network

`zubi config discover` asks the local network for Zubi over multicast DNS (Bonjour), waits two
seconds (`--timeout 5s` waits longer), and lists every Mac that answers, with its URL, addresses
and Zubi version. `--save` stores the URL when exactly one answered; `--pick N` stores the Nth.
The URL uses the Mac's `.local` name when this machine can resolve it, since that outlives an
address change, and its IP address otherwise. `--json` prints the list as JSON.

The Mac announces Zubi only while Zubi listens on all interfaces (**Settings › Server › Network:
All interfaces**), and multicast doesn't cross routers or VPNs. When nothing answers, `discover`
exits 4; use an SSH tunnel or set the URL by hand. On macOS, the first run may ask to allow your
terminal to find devices on the local network.

Each setting is taken from the first place that has it:

1. the flags `--url` and `--key`
2. the environment variables `ZUBI_URL` and `ZUBI_API_KEY`
3. the config file: a pairing from `zubi pair` (see [Through a relay](#through-a-relay)), or the URL
4. for the URL only, the default `http://127.0.0.1:8787`

A URL from a flag or the environment wins over a pairing; `zubi config set url` and
`config discover --save` replace it.

The config file is JSON, written readable by its owner only (mode 0600). Where it lives:

| OS      | Path                                                  |
| ------- | ----------------------------------------------------- |
| macOS   | `~/Library/Application Support/zubi/config.json`     |
| Linux   | `$XDG_CONFIG_HOME/zubi/config.json`, else `~/.config/zubi/config.json` |
| Windows | `%AppData%\zubi\config.json`                          |

`ZUBI_CONFIG` points `zubi` at another file.

Zubi listens on `127.0.0.1:8787` until you change it in its Settings. From another machine, reach
it [through a relay](#through-a-relay), through an SSH tunnel
(`ssh -N -L 8787:localhost:8787 user@mac.local`, then keep the default URL), or over an encrypted
network such as Tailscale. The API is plain HTTP and the key travels in every request; through a
relay, the session around it is end-to-end encrypted.

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
| `health`     | Whether Zubi answers (needs no key), and with a key, its name and access | |
| `key`        | The key's name and access: read-only, or read & write | |
| `config`     | Settings, as above; `config discover` finds Zubi | `--save`, `--pick N` (with `discover`) |
| `version`    | The CLI's version                      | |

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
- An event's URL is `--link`, because `--url` is Zubi's address.

Flags every command accepts, anywhere on the line:

| Flag            | Meaning |
| --------------- | ------- |
| `--json`        | Print the API's JSON response instead of text |
| `--url URL`     | Zubi's address |
| `--key KEY`     | The API key |
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
through Zubi's API or from elsewhere: the Mac, iCloud or another device. Zubi doesn't say which
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
- When the connection drops (Zubi restarts, the Mac sleeps, the network goes), `zubi watch`
  reconnects, waiting a little longer after each failure up to 30 seconds. Zubi sends a
  keep-alive every 30 seconds, so a connection that stays silent for longer is treated as dropped.
  Changes made while it was disconnected are not reported. A refused key (exit 3) or a Zubi too old
  to report changes (exit 4) ends it.
- `--timeout` limits connecting, not how long it watches. Ctrl-C exits 0.

### Subscribing to a calendar

`zubi feed CALENDAR` prints the URL a calendar app (Apple Calendar, Google Calendar, Outlook,
Thunderbird) subscribes to, for a calendar named by title or ID. The app fetches the calendar's
events as iCalendar from Zubi, from 30 days back to a year ahead, so the window moves with time.
`--from` and `--to` fix it instead.

Calendar apps can't send a key in a header, so the URL contains it. **Make a read-only key for
each calendar app** (Settings › Keys) and print the URL with it, so the app can't change anything
and you can revoke it alone:

```sh
ZUBI_API_KEY=<read-only key> zubi feed Work
```

`zubi feed` warns on stderr about which key the URL holds. The URL names the calendar by its ID,
so it keeps working when you rename the calendar. The app must reach the Mac at the URL you
configured; an app that subscribes from a server (Google Calendar) needs Zubi reachable from the
internet, which Zubi isn't built for. `zubi feed CALENDAR --download` writes the iCalendar file to
stdout instead, fetched with the key in a header.

### Using it with Claude Desktop and other MCP clients

`zubi mcp` serves Zubi to AI assistants over the [Model Context
Protocol](https://modelcontextprotocol.io): the client starts it, and talks to it over stdin and
stdout. Give it a key of its own, read-only unless you want the assistant to add and edit.

For Claude Desktop, add it to `claude_desktop_config.json` (**Settings › Developer › Edit
Config**), with the full path to `zubi`:

```json
{
  "mcpServers": {
    "zubi": {
      "command": "/usr/local/bin/zubi",
      "args": ["mcp"],
      "env": {
        "ZUBI_URL": "http://mac.local:8787",
        "ZUBI_API_KEY": "<a key from Zubi › Settings › Keys>"
      }
    }
  }
}
```

For Claude Code:

```sh
claude mcp add zubi --env ZUBI_URL=http://mac.local:8787 --env ZUBI_API_KEY=<key> -- zubi mcp
```

or `claude mcp add zubi -- zubi mcp` to use the URL and key from `zubi config`. Other clients
take the same command, `zubi mcp`.

The tools are `list_reminder_lists`, `list_reminders`, `list_calendars`, `list_events`,
`list_note_folders`, `list_notes`, `get_note`, `create_reminder`, `update_reminder`,
`create_event`, `update_event`, `create_note`, `update_note` and `get_key`. Their arguments are
the API's parameters, by the same names; they answer with the API's JSON, and with an explanation
the assistant can act on when Zubi refuses (a read-only list, a read-only key, a locked note).
The assistant sees the same lists, calendars and folders as `zubi`, and can't delete anything
either. `zubi mcp` logs to stderr, which clients keep in their MCP logs; it starts without a key,
and then each tool answers that one is needed.

## Using it from an agent

- **Always pass `--json`.** Text output is for people and may change; with `--json`, stdout is the
  API's response body, indented, and nothing else. Its shape is the one the API documents (below).
  Fields without a value are left out, not `null`.
- **Branch on the exit code**, not on the text:

  | Code | Meaning |
  | ---- | ------- |
  | 0    | Success; stdout holds the result |
  | 1    | Any other error: a read-only list, calendar or folder, a read-only key, or access not granted on the Mac (403); a locked note (409); Notes unreadable (503); a request the server refused, such as a bad date range (400) |
  | 2    | Bad usage: unknown command or flag, invalid value, missing title or required flag, an edit with nothing to change, conflicting flags (`--due` with `--clear-due`, `--text` with `--html`), or no API key configured. Nothing was sent. |
  | 3    | The API key was refused (401) |
  | 4    | Not found or not shared (404): an unknown list, calendar, folder, reminder, event or note, or one set to Off in Zubi's Settings; `config discover` found no Zubi |
  | 5    | Nothing answered at the URL: Zubi isn't running, wrong address, or no tunnel |

- **On failure, stdout is empty and stderr carries one line** starting `zubi:` that says what went
  wrong, including the server's message when it sent one.
- Pass the key through `ZUBI_API_KEY` or the config file rather than `--key`, which other users of
  the machine can see in the process list.
- `zubi health` exits 0 when Zubi is up, without needing the key; `zubi health --json` prints `ok`.
- `zubi key --json` prints `{"name":"…","access":"read"}` or `"readWrite"`: whether this key may
  write at all.
- `zubi watch --json` prints one JSON object per line as things change, and runs until it is
  killed; read it line by line.

### Changing things from an agent

- **IDs come from the listings**: `.reminders[].id` from `zubi reminders --json`, `.events[].id`
  and `.start` from `zubi events --json`, `.notes[].id` from `zubi notes --json`. Lists, calendars
  and folders can be named by title or ID.
- **Check `writable` first.** `zubi lists --json`, `zubi calendars --json` and `zubi folders --json`
  say for each one whether Zubi may change it. Writing to one that isn't answers 403 (exit 1);
  retrying won't help. Tell the user to set it to Read & Write in Zubi's Settings on the Mac. A
  read-only key (`zubi key --json` says `"access": "read"`) gets 403 for every write, whatever
  `writable` says.
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
  3) echo "API key refused" >&2 ;;
  5) echo "Zubi is not reachable" >&2 ;;
  *) echo "failed" >&2 ;;
esac
```

## Through a relay

A relay is where a Mac running Zubi and its clients meet when they are on different networks, for
example a MacBook on the road and a Raspberry Pi at home. Both connect out to the relay, so neither
needs to accept connections, and the relay copies messages between them. Each session is end-to-end
encrypted between the Mac and the device, with the device's key: the relay can't read your data, and
sees only which addresses connect, when, and how much they send.

1. Run a relay, or use one you already have: see [Running a relay](#running-a-relay).
2. On the Mac, under **Settings › Server › Relay**, turn on **Connect through a relay**, enter the
   relay's `wss://` address and click **Apply**. The status turns to **Connected**.
3. Under **Settings › Keys**, open the device's key's menu and choose **Copy Pairing Code**.
4. On the device:

   ```sh
   zubi pair -        # paste the code, then Enter; it stays out of your shell history
   zubi lists         # every command now reaches the Mac through the relay
   ```

`zubi pair` asks the Mac, through the relay, which key the code is for, and saves it (with the key)
only when that works, so a typo or a stopped Mac shows at once. It exits 3 when the Mac refuses the
key (deleted, or regenerated since the code was copied) and 5 when the Mac isn't connected to the
relay. The code holds the key, so treat it as secret.

From then on every command, `zubi watch` and `zubi mcp` go through the relay. `zubi config` shows the
relay and room, `zubi config unset relay` forgets them, and `--url` or `ZUBI_URL` still reach Zubi
directly for one run. A calendar subscription can't go through the relay, since the calendar app is
what connects: run `zubi tunnel` on its computer and use `zubi feed --url http://127.0.0.1:8787`.

### Lending the relay to other programs

```sh
zubi tunnel                        # http://127.0.0.1:8787, until Ctrl-C
zubi tunnel --listen 127.0.0.1:9000
```

`zubi tunnel` serves Zubi's API at a local address, each connection a session through the relay, so a
dashboard, a script with `curl`, or anything else that speaks HTTP reaches the Mac as if it were on
this computer. Requests still need a key in their `Authorization` header, as they would on the Mac:
the tunnel only carries them. It listens on loopback unless `--listen` says otherwise. To keep it
running on Linux, a service like the relay's below works, with `ExecStart=/usr/local/bin/zubi tunnel`
and `User=` the account that ran `zubi pair` (the pairing is in its config file).

## Running a relay

```sh
zubi relay
```

With no flags, `zubi relay` finds this network's public IPv4 address, gets a certificate for it from
Let's Encrypt, serves `wss://` on port 443, prints the address to use, and renews the certificate
(which lasts six days) on its own. There is nothing to configure, but Let's Encrypt has to reach port
443 of that address from the internet: it checks by connecting there. If it can't, the relay says why
and exits; it never falls back to unencrypted `ws://`.

```text
Relay ready: wss://203.0.113.7
```

| Flag | Does |
| --- | --- |
| `--listen ADDR` | Listen here instead of `:443`. Let's Encrypt still connects to port 443, so forward that to this port. |
| `--ip IP` | Certify this address instead of the one found automatically (IPv6 works too). |
| `--domain NAME` | Certify a domain name pointing at this machine, such as a free DuckDNS name, so the address survives a new IP from your provider. |
| `--state DIR` | Where the certificate and Let's Encrypt account key are kept (default `~/.config/zubi/relay`, readable only by you). |
| `--acme-directory URL` | Another ACME certificate authority, such as Let's Encrypt's staging one for testing. |
| `--behind-tls-proxy` | Serve plain HTTP on `$PORT` (else `:8080`) for a hosting platform or reverse proxy that serves `wss://` itself. WebSockets the proxy didn't receive over TLS are refused. |
| `--no-tls` | Serve plain `ws://` on `$PORT` (else `:8080`). Only on a network you trust: the content stays end-to-end encrypted, but the metadata doesn't. |

It stops on Ctrl-C or SIGTERM, exits with 1 when it can't start (no certificate, port taken) and
with 2 on bad flags. The log goes to stderr: a line when a Mac connects or leaves, with the start of
its room ID, and certificate renewals. It logs no client addresses.

### Where to host it

Pick the first that fits:

1. **A machine at home, if you can forward a port.** A Raspberry Pi or anything else that's always on.
   Nobody else is involved, and it costs nothing. Forward TCP port 443 on your router to the machine,
   run `zubi relay` there as a service, and you're done. Two things to check: your connection must
   have its own public IPv4 address (not carrier-grade NAT, common on mobile and some fibre lines;
   if Let's Encrypt can't connect even with the port forwarded, ask your provider), and when your
   provider changes that address, the relay's URL changes with it. The relay notices within an hour
   and gets a new certificate, but the Mac and clients still have the old address; a free
   [DuckDNS](https://www.duckdns.org) name with `--domain` avoids that.
2. **Render's free web service, if you can't forward a port.** Render serves `wss://` with its own
   certificate, so nothing at home is exposed. It sees the same metadata a relay does, never the
   content. Free services sleep after 15 minutes without traffic; a connected Mac pings every 30
   seconds, which keeps it awake, and 750 free hours a month cover one service running all month. If it does sleep,
   the first connection takes about a minute.
3. **A small server with a public IP address**, if you have one or want one: any VPS, or Oracle
   Cloud's Always Free tier. Run `zubi relay` as at home; the address never changes. Oracle may
   reclaim an Always Free machine that stays idle for a week, and a relay is mostly idle.
4. **Your own reverse proxy or a Cloudflare Tunnel**, if you already run one with a domain: point it at
   `zubi relay --behind-tls-proxy --listen 127.0.0.1:8080`. The proxy must pass WebSocket upgrades on
   and set `X-Forwarded-Proto` (Caddy and `cloudflared` do both).

Not a good fit: Google Cloud Run cuts every WebSocket after at most an hour, and its free tier needs a
billing account that a connection open around the clock would soon draw on.

#### As a service on Linux

Save as `/etc/systemd/system/zubi-relay.service`, then
`sudo systemctl enable --now zubi-relay` and `journalctl -u zubi-relay` to see its address:

```ini
[Unit]
Description=Zubi relay
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=/usr/local/bin/zubi relay --listen :8443 --state /var/lib/zubi-relay
DynamicUser=yes
StateDirectory=zubi-relay
Restart=on-failure
# Let's Encrypt allows five failed checks per address an hour, so don't retry faster.
RestartSec=15min

[Install]
WantedBy=multi-user.target
```

This listens on 8443, so the router forwards its port 443 to the machine's 8443 and the relay needs
no privileges. To listen on 443 directly, use `--listen :443` and add
`AmbientCapabilities=CAP_NET_BIND_SERVICE` under `[Service]`.

#### On Render

New › Web Service › Public Git Repository `https://github.com/fa-krug/zubi`, then:

| Setting | Value |
| --- | --- |
| Language | Go |
| Root Directory | `cli` |
| Build Command | `go build -o zubi ./cmd/zubi` |
| Start Command | `./zubi relay --behind-tls-proxy` |
| Instance Type | Free |
| Health Check Path | `/health` (under Advanced) |

The relay's address is the service's URL with `wss://`, such as `wss://zubi-relay.onrender.com`.
Deploying again picks up a newer `zubi`.

## The API

`zubi` is a thin client. The API it calls is described by an OpenAPI document that Zubi serves
itself:

- `http://<mac>:8787/docs` — Swagger UI, for reading and trying requests
- `http://<mac>:8787/openapi.json` — the OpenAPI 3.1 document

The CLI's tests check its requests, request bodies and response types against that document, so
the two don't drift apart.

## Development

The CLI is a Go module in `cli/` of the Zubi repository. It uses only the standard library, except
for the relay's certificates: `internal/relay` uses `golang.org/x/crypto/acme`.

```sh
cd cli
go test ./...
go build -ldflags "-X main.version=1.2.3" ./cmd/zubi
```

Every request the CLI can make is one entry in `apiCommands` (`cmd/zubi/commands.go`): its method,
path, query parameters, request body type, response type and media type. The tests read
`Packages/ZubiAPI/Sources/ZubiAPI/Resources/openapi.json` and fail when an entry, a path or query
parameter, a request body struct (field names, required vs. optional, which fields may be `null`)
or a response struct disagrees with it, or when the spec gains an operation or a query parameter
no command sends. They also validate the fixtures in `cmd/zubi/testdata/` against it, and run every
command against a fake Zubi that checks each request's method, path, parameters, body and `Accept`
header against the spec and answers with those fixtures.

The MCP tools (`mcpTools` in `cmd/zubi/mcp.go`) each name an entry in `apiCommands` and send
through the same client. The tests check that each tool's arguments are exactly its operation's
parameters and body properties (names, types, enums, bounds, required), that every operation an
assistant could use has a tool, and drive every tool through `zubi mcp` against the fake. The
multicast DNS encoder and decoder behind `config discover` (`cmd/zubi/dns.go`) are tested with
crafted packets, so no test needs the network.

The relay (`internal/relay`, `internal/websocket`) has its own tests: WebSocket framing, the relay
protocol from registration to a closed session, limits and STUN. Sessions through it
(`internal/noise`, `internal/tunnel`) are tested against vectors from an independent Noise
implementation, shared with the app's tests, and end to end: `zubi pair`, commands, `zubi watch`
and `zubi tunnel` run against the fake Zubi through a real relay and a Go stand-in for the Mac
(`tunnel.Host`). Its certificate tests need Pebble,
Let's Encrypt's test CA, and are skipped without it:
`ZUBI_PEBBLE=$(go env GOPATH)/bin/pebble go test ./internal/relay/` after
`go install github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1`. The protocol and the relay's rules
are in [docs/features/relay.md](../docs/features/relay.md).
