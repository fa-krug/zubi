# Zubi

A macOS menu bar app (macOS 14+) that shares selected Reminders lists, calendars and Notes folders
with paired devices, such as a Raspberry Pi, from anywhere. There is one server, the Mac, and any
number of clients, the `zubi` command (`cli/`, Go), and they meet at a relay: `wss://relay.fa-krug.de`
by default, or the user's own `zubi relay`. Each client's session is end-to-end encrypted with its
pairing code's key and ends in the app's HTTP server on `127.0.0.1`, which nothing else reaches; there
is no public API. SwiftUI + AppKit, running as an accessory with no Dock icon (`LSUIElement`). One
third-party dependency, Hummingbird, for the HTTP server. Not sandboxed. The protocol inside a session
is described by [`openapi.json`](Packages/ZubiAPI/Sources/ZubiAPI/Resources/openapi.json), which both
test suites hold the server and the client to.

## Where things are

| Folder | Holds |
| --- | --- |
| `Sources/Settings/` | The Settings window: a sidebar of `SettingsTab`s (General, Connection, Clients, Activity, Reminders, Calendars, Notes, About; Connection is `ServerPane`, Clients `KeysPane`) and one `Form` pane each under `Panes/` |
| `Sources/App/` | `@main` (`ZubiApp`), `BridgeController` (owns the server, access state and settings), the menu bar popover, `KeyStore` (the clients' keys, one Keychain item), `ActivityLog`, `KeepAwake`, `AppPaths` (`Bonjour.swift` is unused and due for removal) |
| `Sources/EventKit/` | `RemindersService` and `CalendarService`, actors over their own `EKEventStore`, conforming to the package's source protocols |
| `Sources/Notes/` | `NotesService` and `JXARunner`: Notes read through `osascript` JavaScript for Automation |
| `Packages/ZubiAPI/` | everything HTTP, as a local Swift package: the server, routes, bearer-key middleware, DTOs, the source protocols, `openapi.json`, the Mac's side of the relay (`Relay/`: Noise, pairing codes, `RelayHost`), and the tests (`swift test`) |
| `cli/` | the `zubi` client and relay, a Go module (`github.com/fa-krug/zubi/cli`) on the standard library plus `golang.org/x/crypto` for the relay's ACME client; every request it makes is in `cmd/zubi/commands.go`, pairing and status in `pair.go` and `status.go`, the relay is `internal/relay`, sessions through it `internal/tunnel` and `internal/noise` |
| `Sources/Updates/` | the self-updater, ported from Blitz: `Model/`, `Service/`, `UI/` |
| `Scripts/` | `verify-signature.sh` and `set-release-secrets.sh` for releases, `gen-notice.py` for `NOTICE.md` |
| `site/` | the website at <https://zubi.fa-krug.de/>: page bodies in `pages/`, shared styles in `assets/`, `install.sh` (the CLI's installer, published as is), and `tools/build.py`, which wraps the pages in the shared layout |
| `.github/workflows/` | `tests.yml` (the drift gate), `release.yml` (the app), `release-cli.yml` (the CLI), `pages.yml` (the website) |
| `docs/` | everything longer than a line here; start at [docs/README.md](docs/README.md) |

## Build

XcodeGen owns the project. `Zubi.xcodeproj` (including `Package.resolved`) is generated from
`project.yml` and **committed**, because the release workflow builds it as-is. After editing
`project.yml`, run `xcodegen generate` and commit both. Never hand-edit `Zubi.xcodeproj`.
`Sources/Info.plist` and `Sources/Zubi.entitlements` are hand-written and committed.

```sh
xcodebuild -project Zubi.xcodeproj -scheme Zubi -configuration Debug \
    -derivedDataPath build/DerivedData build
# -> build/DerivedData/Build/Products/Debug/Zubi Dev.app
```

Local builds sign with the `Zubi Self-Signed` identity, created once per machine
([signing.md §1](docs/signing.md#1-create-the-zubi-self-signed-identity-once)). Without it the build
fails to sign; with an ad-hoc signature instead, macOS forgets the Reminders, Calendar and
Automation grants on every rebuild.

Tests, all of which CI runs before either release:

```sh
(cd Packages/ZubiAPI && swift test)   # every route against openapi.json, with fake sources
(cd cli && go vet ./... && go test ./...)   # every CLI request and struct against openapi.json
```

The relay's certificate tests also need `ZUBI_PEBBLE` set to a Pebble binary
([relay.md](docs/features/relay.md#tests)); without it they are skipped, and CI runs them.

Full local loop: [development.md](docs/development.md).

## Non-negotiables

- **A push to `main` ships what it changed.** A change under the app's paths (`Sources/`,
  `Packages/`, `project.yml`, `Zubi.xcodeproj`, `NOTICE.md`) releases the app as `vX.Y.Z`; a change
  under `cli/` releases the CLI as `cli/vX.Y.Z`. Both run `tests.yml` first and publish nothing if it
  fails. A change under `site/` redeploys the website and nothing else. Never
  push a change to `main` that doesn't build. See [release.md](docs/release.md).
- **`openapi.json` is the contract between the app and the client.** Change an endpoint, parameter
  or response shape there first, then the server and the CLI until both test suites pass. The tests fail on drift in either
  direction: an undocumented route or field, or a documented one the code doesn't produce.
- **Debug builds are their own channel**: `Zubi Dev.app`, bundle id `de.fa-krug.zubi.dev`. They get
  their own prefs, caches, login item and TCC grants, and never update themselves. Anything newly
  persisted stays keyed by `Bundle.main.bundleIdentifier` (`AppPaths.caches()` for files).
- **Nothing is exposed by default, and nothing is writable unless chosen.** Each list, calendar and
  folder is Off, Read or Read & Write in Settings, checked on every request: Off answers 404 for reads
  and writes alike, Read answers 403 to a write. A move needs the target writable too. Nothing
  ever deletes. Every `/api` route sits behind the bearer-key middleware; only `/health` does not.
  The server binds `127.0.0.1` only: clients reach it through relay sessions, never directly.
- **A protected resource needs its usage string *and* its entitlement.** Under the hardened runtime
  (Release only) a missing entitlement denies silently with no prompt. Today that is
  `personal-information.calendars` (Reminders and Calendar) and `automation.apple-events` (Notes).
  See [signing.md](docs/signing.md#hardened-runtime).
- **Notes script arguments go through `run(argv)`**, never interpolated into JXA source.
- **The relay holds no key and never falls back to plain.** Sessions through it are end-to-end
  encrypted between the Mac and the device, with the device key, and end in Zubi's own server, so the
  key check and Settings apply as to any request. Without a certificate the relay exits; plain `ws://`
  takes `--no-tls`, and neither the app nor `zubi` uses a `ws://` relay. See
  [relay.md](docs/features/relay.md#invariants).
- **`BundleSignature.developerID` is read by the release workflow.** Keep it a single triple-quoted
  string literal, and change it only together with the signing team.
- **The update feed is `fa-krug/zubi`**, named once in `ReleaseFeed.repository`. Release assets must
  stay `Zubi-Universal-<version>.zip`, zipped with `ditto -c -k --keepParent --sequesterRsrc`. CLI
  releases share the repository but never take the Latest badge, and their `cli/v…` tags don't parse
  as app versions, so the updater never offers one.

## Keep in sync

Any doc your change makes wrong is fixed in the same commit:

| When you change | Update |
| --- | --- |
| an endpoint, a query parameter, a response shape | `openapi.json` first, then the client in `cli/`, and [cli/README.md](cli/README.md) and `site/pages/docs.html` where a user sees it |
| the setup flow, install, or what a user sees first | [README.md](README.md) (for end users) and the home page's steps in `site/pages/index.html` |
| the toolchain, repository layout, tests, or how to contribute | [CONTRIBUTING.md](CONTRIBUTING.md) |
| a CLI command, flag or exit code | [cli/README.md](cli/README.md), the usage text in `cli/cmd/zubi/main.go`, and `site/pages/docs.html` |
| anything a user sees in Settings, the setup flow, or what the app sends where | `site/pages/docs.html`, the home page's setup steps, and `site/pages/privacy.html` if a connection changes |
| `project.yml`, the toolchain, the build command, the dev channel | [development.md](docs/development.md) |
| a workflow, the version scheme, the secrets | [release.md](docs/release.md) |
| the CLI's release archive names or platforms | `site/install.sh`, which downloads them by name |
| entitlements, usage strings, signing identities, `BundleSignature` | [signing.md](docs/signing.md) |
| anything under `Sources/Updates/` | [features/updates.md](docs/features/updates.md) |
| the relay's protocol, limits, certificates or flags, the session handshake, pairing codes | [features/relay.md](docs/features/relay.md), the relay sections of [cli/README.md](cli/README.md) and `site/pages/docs.html`; for the handshake, key derivation or pairing format, regenerate `Packages/ZubiAPI/Tests/Fixtures/relay-noise-vectors.json` and keep the Go and Swift sides passing it |
| a Swift package, or a Hummingbird bump | [NOTICE.md](NOTICE.md): check `LINKED` in `Scripts/gen-notice.py` against the build's `Zubi.LinkFileList`, then run `Scripts/gen-notice.py build/DerivedData/SourcePackages/checkouts` |

## Before you finish

- The Debug build compiles with **no new warnings**, and both test suites pass.
- You ran the app and exercised what you changed, including `zubi` against any endpoint you touched.
- `project.yml` changes are regenerated and the result is in the commit.
- Docs are in sync, per the table above.
