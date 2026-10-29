# Zubi

A macOS menu bar app (macOS 14+) that serves selected Reminders lists, calendars and Notes folders
over a small authenticated HTTP API, so devices like a Raspberry Pi can read them. SwiftUI + AppKit,
running as an accessory with no Dock icon (`LSUIElement`). One third-party dependency, Hummingbird,
for the HTTP server. Not sandboxed. The same repository holds `zubi`, a Go command-line client for
the API (`cli/`). The API's contract is
[`openapi.json`](Packages/ZubiAPI/Sources/ZubiAPI/Resources/openapi.json); the app serves it at
`/openapi.json` with Swagger UI at `/docs`.

## Where things are

| Folder | Holds |
| --- | --- |
| `Sources/Settings/` | The Settings window: a sidebar of `SettingsTab`s (General, Server, Keys, Activity, Reminders, Calendars, Notes, About) and one `Form` pane each under `Panes/` |
| `Sources/App/` | `@main` (`ZubiApp`), `BridgeController` (owns the server, access state and settings), the menu bar popover, `KeyStore` (the per-device keys, one Keychain item), `ActivityLog`, `KeepAwake`, `Bonjour`, `AppPaths` |
| `Sources/EventKit/` | `RemindersService` and `CalendarService`, actors over their own `EKEventStore`, conforming to the package's source protocols |
| `Sources/Notes/` | `NotesService` and `JXARunner`: Notes read through `osascript` JavaScript for Automation |
| `Packages/ZubiAPI/` | everything HTTP, as a local Swift package: the server, routes, bearer-key middleware, DTOs, the source protocols, `/openapi.json` and `/docs`, and the contract tests (`swift test`) |
| `cli/` | the `zubi` CLI and relay, a Go module (`github.com/fa-krug/zubi/cli`) on the standard library plus `golang.org/x/crypto` for the relay's ACME client; every request it makes is in `cmd/zubi/commands.go`, the relay is `internal/relay` |
| `Sources/Updates/` | the self-updater, ported from Blitz: `Model/`, `Service/`, `UI/` |
| `Scripts/` | `verify-signature.sh` and `set-release-secrets.sh` for releases, `gen-notice.py` for `NOTICE.md` |
| `site/` | the website at <https://fa-krug.github.io/zubi/>: page bodies in `pages/`, shared styles in `assets/`, and `tools/build.py`, which wraps the pages in the shared layout and generates `api.html` from `openapi.json` |
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
  fails. A change under `site/` (or to `openapi.json`) redeploys the website and nothing else. Never
  push a change to `main` that doesn't build. See [release.md](docs/release.md).
- **`openapi.json` is the contract.** Change an endpoint, parameter or response shape there first,
  then the server and the CLI until both test suites pass. The tests fail on drift in either
  direction: an undocumented route or field, or a documented one the code doesn't produce.
- **Debug builds are their own channel**: `Zubi Dev.app`, bundle id `de.fa-krug.zubi.dev`. They get
  their own prefs, caches, login item and TCC grants, and never update themselves. Anything newly
  persisted stays keyed by `Bundle.main.bundleIdentifier` (`AppPaths.caches()` for files).
- **Nothing is exposed by default, and nothing is writable unless chosen.** Each list, calendar and
  folder is Off, Read or Read & Write in Settings, checked on every request: Off answers 404 for reads
  and writes alike, Read answers 403 to a write. A move needs the target writable too. The API never
  deletes. Every `/api` route sits behind the
  bearer-key middleware; only `/health` does not. The default bind is `127.0.0.1`.
- **A protected resource needs its usage string *and* its entitlement.** Under the hardened runtime
  (Release only) a missing entitlement denies silently with no prompt. Today that is
  `personal-information.calendars` (Reminders and Calendar) and `automation.apple-events` (Notes).
  See [signing.md](docs/signing.md#hardened-runtime).
- **Notes script arguments go through `run(argv)`**, never interpolated into JXA source.
- **The relay holds no key and never falls back to plain.** Without a certificate it exits; plain
  `ws://` takes `--no-tls`. See [relay.md](docs/features/relay.md#invariants).
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
| an endpoint, a query parameter, a response shape | `openapi.json` first, then `site/pages/docs.html` and [cli/README.md](cli/README.md); the site's API reference is generated |
| the setup flow, install, or what a user sees first | [README.md](README.md) (for end users) and the home page's steps in `site/pages/index.html` |
| the toolchain, repository layout, tests, or how to contribute | [CONTRIBUTING.md](CONTRIBUTING.md) |
| a CLI command, flag or exit code | [cli/README.md](cli/README.md), the usage text in `cli/cmd/zubi/main.go`, and `site/pages/docs.html` |
| anything a user sees in Settings, the setup flow, or what the app sends where | `site/pages/docs.html`, the home page's setup steps, and `site/pages/privacy.html` if a connection changes |
| `project.yml`, the toolchain, the build command, the dev channel | [development.md](docs/development.md) |
| a workflow, the version scheme, the secrets | [release.md](docs/release.md) |
| entitlements, usage strings, signing identities, `BundleSignature` | [signing.md](docs/signing.md) |
| anything under `Sources/Updates/` | [features/updates.md](docs/features/updates.md) |
| the relay's protocol, limits, certificates or flags | [features/relay.md](docs/features/relay.md), the relay section of [cli/README.md](cli/README.md) and `site/pages/docs.html` |
| a Swift package, or a Hummingbird bump | [NOTICE.md](NOTICE.md): check `LINKED` in `Scripts/gen-notice.py` against the build's `Zubi.LinkFileList`, then run `Scripts/gen-notice.py build/DerivedData/SourcePackages/checkouts` |

## Before you finish

- The Debug build compiles with **no new warnings**, and both test suites pass.
- You ran the app and exercised what you changed, including a `curl` against any endpoint you touched.
- `project.yml` changes are regenerated and the result is in the commit.
- Docs are in sync, per the table above.
