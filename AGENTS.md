# Zubi

A macOS menu bar app (macOS 14+) that serves selected Reminders lists, calendars and Notes folders
over a small authenticated HTTP API, so devices like a Raspberry Pi can read them. SwiftUI + AppKit,
running as an accessory with no Dock icon (`LSUIElement`). One third-party dependency, Hummingbird,
for the HTTP server. Not sandboxed. The API is documented in [README.md](README.md#api).

## Where things are

| Folder | Holds |
| --- | --- |
| `Sources/Settings/` | The Settings window: a sidebar of `SettingsTab`s (General, Server, API Key, Reminders, Calendars, Notes, About) and one `Form` pane each under `Panes/` |
| `Sources/App/` | `@main` (`ZubiApp`), `BridgeController` (owns the server, access state and settings), the menu bar popover, the Keychain-backed API key, `AppPaths` |
| `Sources/Server/` | the Hummingbird server, bearer-key middleware, and one routes file per source |
| `Sources/EventKit/` | `RemindersService` and `CalendarService`, actors over their own `EKEventStore` |
| `Sources/Notes/` | `NotesService` and `JXARunner`: Notes read through `osascript` JavaScript for Automation |
| `Sources/Models/` | the JSON DTOs and date formatting the API returns |
| `Sources/Updates/` | the self-updater, ported from Blitz: `Model/`, `Service/`, `UI/` |
| `Scripts/` | `verify-signature.sh` and `set-release-secrets.sh`, both used for releases |
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

Full local loop: [development.md](docs/development.md).

## Non-negotiables

- **Every push to `main` ships.** `.github/workflows/release.yml` builds, signs with Developer ID,
  notarizes and publishes a GitHub Release that installed copies update to. Only pushes touching
  nothing but `**/*.md` or `docs/**` skip it. Never push a change to `main` that doesn't build.
  See [release.md](docs/release.md).
- **Debug builds are their own channel**: `Zubi Dev.app`, bundle id `de.fa-krug.zubi.dev`. They get
  their own prefs, caches, login item and TCC grants, and never update themselves. Anything newly
  persisted stays keyed by `Bundle.main.bundleIdentifier` (`AppPaths.caches()` for files).
- **Nothing is exposed by default.** The API serves only the lists, calendars and folders ticked in
  Settings, checked on every request; anything else returns 404. Every `/api` route sits behind the
  bearer-key middleware; only `/health` does not. The default bind is `127.0.0.1`.
- **A protected resource needs its usage string *and* its entitlement.** Under the hardened runtime
  (Release only) a missing entitlement denies silently with no prompt. Today that is
  `personal-information.calendars` (Reminders and Calendar) and `automation.apple-events` (Notes).
  See [signing.md](docs/signing.md#hardened-runtime).
- **Notes script arguments go through `run(argv)`**, never interpolated into JXA source.
- **`BundleSignature.developerID` is read by the release workflow.** Keep it a single triple-quoted
  string literal, and change it only together with the signing team.
- **The update feed is `fa-krug/zubi`**, named once in `ReleaseFeed.repository`. Release assets must
  stay `Zubi-Universal-<version>.zip`, zipped with `ditto -c -k --keepParent --sequesterRsrc`.

## Keep in sync

Any doc your change makes wrong is fixed in the same commit:

| When you change | Update |
| --- | --- |
| an endpoint, a query parameter, a response shape, the setup flow | [README.md](README.md) |
| `project.yml`, the toolchain, the build command, the dev channel | [development.md](docs/development.md) |
| `release.yml`, the version scheme, the secrets | [release.md](docs/release.md) |
| entitlements, usage strings, signing identities, `BundleSignature` | [signing.md](docs/signing.md) |
| anything under `Sources/Updates/` | [features/updates.md](docs/features/updates.md) |
| a Swift package, or a Hummingbird bump | [NOTICE.md](NOTICE.md): check `LINKED` in `Scripts/gen-notice.py` against the build's `Zubi.LinkFileList`, then run `Scripts/gen-notice.py build/DerivedData/SourcePackages/checkouts` |

## Before you finish

- The Debug build compiles with **no new warnings**.
- You ran the app and exercised what you changed, including a `curl` against any endpoint you touched.
- `project.yml` changes are regenerated and the result is in the commit.
- Docs are in sync, per the table above.
