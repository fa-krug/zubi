# Updates

By default Zubi checks this repo's GitHub Releases once a day, offers the newest release in a native
window with its release notes, installs it and relaunches. There is no Sparkle and no appcast: the
public release list is the feed. The code lives in `Sources/Updates/`, ported from Blitz's updater.

## Invariants

- **The archive is a zip, never a DMG.** A zip expands with `ditto`; a DMG would have to be mounted,
  which means a volume, a Spotlight handle and a detach that can fail. A release published without a
  zip is not installable and is not offered.
- **The zip is chosen by architecture.** A release carries one `-Universal-` zip, which every Mac
  takes. Intel is offered *nothing* without one, since a thin build would install and then refuse to
  launch; Apple silicon would prefer a thin zip if a release ever carried one.
- **Nobody runs `xattr` after the first install.** An archive Zubi fetched itself is not quarantined:
  macOS sets that flag for sandboxed downloaders and for apps that opt in with
  `LSFileQuarantineEnabled`, and Zubi is neither. `Quarantine` checks anyway through
  `getxattr`/`removexattr` rather than the `xattr` tool, and an app that still carries the flag is
  refused rather than installed.
- **The signature is the only integrity guarantee.** A downloaded bundle is trusted when its seal
  validates, nested code included, *and* it satisfies the Developer ID requirement pinned in
  `BundleSignature`. The requirement names team `HS26J3YA63`, so a certificate renewal strands
  nobody, and the release workflow checks every build against it before publishing. `notarized` is
  deliberately not in the requirement: it resolves the ticket through `syspolicyd` or the network, so
  an offline Mac would refuse a bundle the chain already proves is ours.
- **A build only ever updates within its own channel.** `de.fa-krug.zubi.dev` never updates at all:
  **Check for Updates…** reports a local build, and the automatic check never starts.
- **Nothing is installed unless every check passes.** Bundle id, version and signature are all checked
  on the expanded copy before `replaceItemAt` runs, and the running app survives any failure untouched.
- **Relaunching goes through `NSApp.terminate`, never `exit`.** That cancels the server task, so the
  port is released before the new copy starts and tries to bind it.
- **Automatic checking is optional; Check for Updates never is.** Setup › 8. Updates › Automatically
  check for updates (`automaticallyCheckForUpdates` in `UserDefaults`, default on) is the one update
  preference. Turning it off cancels the pump, and `presentIfAvailable` refuses while it is off, so
  neither a late response nor the cached release can raise the window. Release metadata and the
  skipped version stay in the feature's own cache file.
- **The window shows the changelog and nothing else.** A body may carry install instructions below
  `<!-- zubi:install -->`, and `ReleaseNotes.summary`, the single reader of that marker, drops them.
  It is called where the feed is parsed, so the cache holds the cut text too. CI publishes GitHub's
  generated changelog without the marker, so it comes back whole.
- **`@handle` and `#123` are linked by the app, never by the release body.** GitHub autolinks both on
  the web, and a bare mention is what notifies the contributor, so the published body keeps them
  plain and `ReleaseNotes` spells them as Markdown links on the way to the window. Both point at
  `ReleaseFeed.repository` (`fa-krug/zubi`), the one place the repo is named.

## Channel and version

`ReleaseChannel` is derived from the bundle id, and nothing else:

| Bundle id | Channel | Takes |
| --- | --- | --- |
| `de.fa-krug.zubi` | `.stable` | releases, never prereleases |
| anything else | `.development` | nothing |

There is no beta channel. Because stable refuses a GitHub prerelease, one can be published by hand to
test the feed without anyone being offered it.

`AppVersion` parses `MAJOR.MINOR.PATCH` (and, inherited from Blitz, `MAJOR.MINOR.PATCH-beta.N`) with
semver precedence. Everything else parses to nil, so an off-shape tag can never be offered as an
update. A release whose tag disagrees with its `prerelease` flag is treated as mis-published and
skipped.

Intel is *not* a channel. Every release is one universal build under the same tag, version, bundle
id and signature; `ReleaseArchitecture` picks the asset, and nothing about identity changes.

## Checking

`UpdateCheckStore` owns the check: a private `.ephemeral`, `urlCache = nil` session (never
`URLSession.shared`), a self-rescheduling pump, and one atomic JSON file.

```text
~/Library/Caches/<bundle-id>/update-check.json
```

It holds `lastCheckedAt`, the newest release seen, and the version the user dismissed. Freshness is
measured from `lastCheckedAt`, so relaunching never re-asks GitHub. The interval is 24 h, dropping to
2 h after a failed attempt, and the first check waits 30 s so it never lands in the login rush. The
request asks for the latest 20 releases and carries a `User-Agent`, which the GitHub API rejects
requests without.

`UpdateCoordinator` is created with the app, not with the first view, so the daily check runs whether
or not the menu is ever opened. `applyAutomaticChecking()` starts or stops the pump from the
preference, at launch and whenever the toggle changes. Turning it back on resumes the same schedule,
startup delay and cached check time included. `stop()` cancels the pump only: a manual check runs on
its caller's task and finishes either way.

Each version raises the window uninvited at most once per launch. The store keeps Blitz's re-offer
path (a `false` from `onUpdateAvailable` retries every two minutes, up to 15 times), but Zubi has no
activity to defer to: the coordinator withholds a prompt only while automatic checking is off, which
also stops the pump.

**Later means skip.** It records the version, so that release stops asking; a newer one still asks.
**Check for Updates…**, in the menu bar popover and in Setup › 8. Updates, ignores the record and
always offers whatever is newer than what is running. Setup › 8. Updates also shows the running
version.

## The window

`UpdateWindowController` hosts `UpdateWindowView` in a plain `NSWindow` titled "Software Update",
built on first show and torn down on close. Closing it never quits the app. It moves to the active
Space, because a menu bar app's activation never switches Spaces. One window carries every stage:
checking, up to date, local build, available, installing (with progress), ready to relaunch, and
failed. The window sizes itself to the content each stage measures.

## Installing

One route, whatever the install came from:

1. Stream the zip into `~/Library/Caches/<bundle-id>/Updates/`, with real byte progress and a Cancel
   that actually aborts the transfer.
2. `ditto -x -k` it into a staging folder, and take whatever `.app` lands there.
3. Check quarantine natively; clear it if somehow present, and refuse the update if it survives.
4. Verify the bundle id, the version, and that the code signature is valid and proves the bundle is
   ours by the pinned Developer ID requirement.
5. `FileManager.replaceItemAt`. A non-writable `/Applications` is reported, not worked around; there
   is no privileged helper.
6. Offer **Relaunch**, which spawns a detached `/bin/sh` waiter that reopens the app once this process
   exits, then calls `NSApp.terminate`. `open` on a bundle that is still running would only
   re-activate the instance on its way out.

Nothing here touches `~/Library/Preferences`, the keychain or the TCC database, so the exposed lists,
network settings, API key and permission grants all survive an update.

## Releasing into it

`.github/workflows/release.yml` runs on every push to `main` and publishes one asset,
`Zubi-Universal-<version>.zip`, under the tag `v<version>`. The version is `MAJOR.MINOR` from
`project.yml` with the workflow's run number as the patch, so every release is strictly newer than
the last and is never a prerelease. The zip is made with

```sh
ditto -c -k --keepParent --sequesterRsrc "$APP" "dist/$ZIP_FILE"
```

which is the only zip that leaves the code signature verifiable. Plain `zip` drops symlinks and
breaks the seal, and the signature check above would then reject every update. The release body is
GitHub's generated changelog. See [release.md](../release.md).
