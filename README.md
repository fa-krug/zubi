# Zubi

*Zubi* is Basque for "bridge": between your Mac and everything else, in both directions.

<p align="center">
  <img alt="macOS 14 or later"
       src="https://img.shields.io/badge/macOS-14%2B-000000?style=flat&logo=apple&logoColor=white">
</p>

A menu bar app for macOS 14+ that serves selected Reminders lists, calendars and Notes folders over a
small authenticated HTTP API, so devices like a Raspberry Pi can read them.

Apple keeps this data on the Mac and in iCloud, with no API a Linux box can call. Zubi runs where the
data already is, reads it through the system's own frameworks, and hands out exactly the lists,
calendars and folders you tick. Nothing is exposed until you do.

## Install

Download the latest `Zubi-Universal-<version>.zip` from
[Releases](https://github.com/fa-krug/zubi/releases/latest), unzip it and move `Zubi.app` to
`/Applications`. Releases are signed with Developer ID and notarized, so it opens like any other
downloaded app.

From then on Zubi updates itself from the same releases, with no further steps.

## First run

1. Menu bar icon, then **Setup…**
2. **Access:** grant Reminders and Calendar, and connect Notes (Automation prompt)
3. Tick what the API may expose. Nothing is exposed by default
4. Choose bind address and port, then **Apply and restart server**
5. Copy the API key or the ready-made curl command
6. Optionally enable **Launch at login**

The server listens on `127.0.0.1:8787` until you change it. "Localhost only" pairs with an SSH tunnel;
"Specific IP" suits a Tailscale address; "All interfaces" puts the API on every network the Mac is on.

## API

All `/api` routes need `Authorization: Bearer <api-key>`. Hidden items return 404.

| Method | Path | Notes |
|---|---|---|
| GET | `/health` | No auth |
| GET | `/api/lists` | Exposed reminder lists |
| GET | `/api/reminders` | `list`, `due=today\|overdue`, `completed=true` |
| GET | `/api/calendars` | Exposed calendars |
| GET | `/api/events` | `from`, `to` (YYYY-MM-DD, inclusive, default next 7 days), `calendar` |
| GET | `/api/notes/folders` | Exposed Notes folders |
| GET | `/api/notes` | Metadata, newest first. `folder`, `limit` (default 50, max 500) |
| GET | `/api/note?id=...` | Full note as `plaintext` and `html` |

`list`, `calendar` and `folder` take an ID or a name. An event range is limited to one year.

Dates: ISO 8601 in UTC, or `YYYY-MM-DD` in the Mac's time zone for all-day items. Recurring events
come back as individual occurrences.

## Notes specifics

- Apple Notes has no public API. The app runs JavaScript for Automation via `osascript`, which
  requires the Automation permission ("Zubi wants to control Notes") and launches Notes.app if
  needed.
- Locked notes return metadata only.
- Note IDs are `x-coredata://...` URLs, so they go in a query parameter, URL-encoded.
- Slower than EventKit: listing a large folder can take a second or two.

## From the Raspberry Pi

```bash
ssh -N -L 8787:localhost:8787 user@mac.local
curl -H "Authorization: Bearer <key>" "http://localhost:8787/api/events?from=2026-11-02&to=2026-11-06"
curl -G -H "Authorization: Bearer <key>" --data-urlencode "id=x-coredata://..." "http://localhost:8787/api/note"
```

## Notes on hosting

- The app must run in a logged-in GUI session. With FileVault enabled, macOS disables automatic
  login.
- The API speaks plain HTTP. Keep it on localhost behind an SSH tunnel, or on an encrypted network
  such as Tailscale, rather than on a network you don't trust.
- Not sandboxed. If you sandbox it later, add the network server entitlement.

## Updates

Zubi checks this repo's GitHub Releases once a day, shows the release notes when there is something
new, and installs and relaunches when you say so. **Later** skips that version; a newer one still
asks. Turn the daily check off under **Setup… → 8. Updates**. **Check for Updates…** there and in the
menu bar popover works either way. Details: [docs/features/updates.md](docs/features/updates.md).

## Development

See **[docs/development.md](docs/development.md)** for the toolchain and build, and
**[docs/release.md](docs/release.md)** for how a push to `main` becomes an update.
**[docs/](docs/README.md)** indexes the rest.

## Contributing

Read **[CONTRIBUTING.md](CONTRIBUTING.md)** first. Every PR fills in the
**[pull request template](.github/PULL_REQUEST_TEMPLATE.md)**. Security issues go through
[SECURITY.md](SECURITY.md), not the issue tracker.

## Next steps

- Write endpoints (reminders first, then events)
- Change stream via `EKEventStoreChanged` as SSE
- Folder cache for Notes
- Tags and subtasks: port the parsing from FradSer/event (MIT, keep its license notice)
- Optional `/mcp` endpoint with the Swift MCP SDK
