# Release

How a build reaches a user. The local development loop is in [development.md](development.md);
the signing identities are in [signing.md](signing.md).

## Two products, each released on its own change

The repository ships two things from `main`, each from its own workflow, and each only when a push
changes it:

| Workflow | Releases when a push changes | Tag | Marked Latest |
| --- | --- | --- | --- |
| `release.yml` | `Sources/`, `Packages/`, `project.yml`, `Zubi.xcodeproj/`, `NOTICE.md`, `Scripts/verify-signature.sh`, the workflow | `v<version>` | yes |
| `release-cli.yml` | `cli/`, the workflow | `cli/v<version>` | never |

A push that changes only Markdown elsewhere, `docs/` or the other product releases nothing new. A
change to `openapi.json` lives in `Packages/` and so ships the app, which serves it; the CLI's tests
still run against it in that release's gate. Either workflow can also be started by hand
(`workflow_dispatch`).

Both are numbered the same way: `MAJOR.MINOR` from a file (`MARKETING_VERSION` in `project.yml` for
the app, `cli/VERSION` for the CLI), and the patch is that workflow's run number, so each release is
strictly newer than the last. **The app's workflow keeps the file name `release.yml`**: GitHub counts
run numbers per workflow file, so a renamed file would restart at 1 and the updater would see every
new build as older than the installed one.

The website deploys from a third workflow, `pages.yml`, when a push changes `site/`,
`openapi.json` (its API reference is generated from it) or the workflow. It runs
`python3 site/tools/build.py _site` and publishes `_site` to GitHub Pages at
<https://fa-krug.github.io/zubi/>; a newer push cancels a deployment still running. It needs Pages'
source set to GitHub Actions once, under the repository's Settings › Pages.

## The gate: `tests.yml`

Both release workflows call `tests.yml` and publish nothing unless it passes. It also runs on every
pull request. `openapi.json` is the contract, and it is checked from both sides:

- **Server and app** (`macos-26`): `swift test` in `Packages/ZubiAPI` builds the real router with fake
  sources and requires its routes to equal the spec's operations exactly, every operation and every
  documented parameter to answer with a body that validates strictly against the spec, and every
  error it can provoke to be documented. Then an unsigned Debug build of the app proves it still
  compiles against the package.
- **CLI** (`ubuntu-latest`): `gofmt`, `go vet`, and `go test`, whose drift tests require every request
  in `cmd/zubi/commands.go` to exist in the spec with only declared parameters, every response struct
  to match its schema field for field, and the fixtures to validate against the spec. The job installs
  Pebble, Let's Encrypt's test CA, so the relay's certificate tests run against a real ACME server.
  Then `shellcheck` on the website's install script, and a cross-compile for every platform the
  release ships.

## The app

`release.yml`, one job on a `macos-26` runner with Xcode 26, after the gate:

1. **Version.** As above. `CURRENT_PROJECT_VERSION` is the run number too. Bump `MAJOR.MINOR` in
   `project.yml` when a release deserves it, then regenerate the project.
2. **Sign.** The Developer ID Application certificate from `DEVELOPER_ID_P12_BASE64` /
   `DEVELOPER_ID_P12_PASSWORD` is imported into a throwaway keychain, and the build signs with it and
   a secure timestamp. Team `HS26J3YA63` is what keeps users' Reminders, Calendar and Automation
   grants alive across updates, and what the updater trusts.
3. **Build** a universal (`arm64 x86_64`) Release `Zubi.app` from the committed `Zubi.xcodeproj`,
   and assert both slices on `Contents/MacOS/Zubi`. The build resolves packages from the committed
   `Package.resolved`.
4. **`Scripts/verify-signature.sh`** asserts the hardened runtime, an intact seal, no
   `get-task-allow`, and an entitlement for every usage string. The app is then checked against the
   requirement in `Sources/Updates/Service/BundleSignature.swift`, read from the source, so a release
   the updater would refuse never ships.
5. **Notarize** with `notarytool` and the App Store Connect API key in the `NOTARY_API_*` secrets,
   printing Apple's log and failing on anything but `Accepted`; then **staple** the ticket and assert
   `spctl` accepts the app.
6. **Package** `Zubi-Universal-<version>.zip` with `ditto -c -k --keepParent --sequesterRsrc`, the
   only zip that keeps the code signature verifiable.
7. **Publish** a GitHub Release `v<version>` with that zip and GitHub's generated changelog since the
   previous `v` tag, marked `--latest`.

App releases run one at a time (`concurrency: release-app`), and a queued one is never cancelled. The
in-app updater reads those releases directly; see [features/updates.md](features/updates.md). There
is one channel: no beta, no DMG, no Homebrew cask.

## The CLI

`release-cli.yml`, on `macos-26` after the gate, because the macOS binary is signed there; Go
cross-compiles everything else from the same runner with `CGO_ENABLED=0`:

1. **Build** `zubi` for macOS (arm64 and x86_64, joined with `lipo` into one universal binary), Linux
   (amd64, arm64, armv7 for 32-bit Raspberry Pi OS) and Windows (amd64, arm64), with the version
   linked in and symbols stripped.
2. **Sign** the macOS binary with the same Developer ID certificate, hardened runtime and a secure
   timestamp, and **notarize** it. A bare binary cannot carry a stapled ticket, so Gatekeeper checks
   it online the first time it runs.
3. **Package** one archive per platform, `zubi_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows),
   each holding the binary, `README.md`, Zubi's MIT `LICENSE` and Go's `LICENSE.go` for the runtime
   and `golang.org/x/crypto` linked into it (both under that same licence); plus `zubi_<version>_checksums.txt` (SHA-256).
4. **Publish** a GitHub Release `cli/v<version>`, notes since the previous `cli/v` tag, with
   `--latest=false`, so the Latest badge and README's latest-release link keep pointing at the app.

The tag doubles as the Go module version, so `go install github.com/fa-krug/zubi/cli/cmd/zubi@latest`
installs the newest release.

### Distribution channels

Every channel installs the archives on the GitHub Release, pinned by their checksums:

| Channel | Systems | Install |
| --- | --- | --- |
| GitHub Releases | all | download the archive for your system |
| Install script `site/install.sh` | Linux x86-64, ARM64 and ARMv7, macOS | `curl -fsSL https://fa-krug.github.io/zubi/install.sh \| sh` |
| Homebrew tap `fa-krug/homebrew-tap` | macOS, Linux x86-64 and ARM64 | `brew install fa-krug/tap/zubi` |
| Scoop bucket `fa-krug/scoop-bucket` | Windows x86-64 and ARM64 | `scoop bucket add fa-krug https://github.com/fa-krug/scoop-bucket` then `scoop install fa-krug/zubi` |
| Go | anything Go builds for | `go install github.com/fa-krug/zubi/cli/cmd/zubi@latest` |

After publishing, the last step of `release-cli.yml` runs `Scripts/package-manifests.py`, which writes
`Formula/zubi.rb` for the tap and `bucket/zubi.json` for the bucket from the release's version and
checksums, and pushes both. It needs:

1. The two public repositories, `fa-krug/homebrew-tap` and `fa-krug/scoop-bucket`. They exist, seeded
   by hand with 0.1.1; every later release rewrites their files.
2. A fine-grained token with **Contents: Read and write** on those two repositories only, stored in
   this repository as the secret `PACKAGES_TOKEN`.

Until the secret exists the step prints a notice and skips, so a CLI release never fails for it.
Homebrew on Linux supports only x86-64 and 64-bit ARM, so 32-bit Raspberry Pi OS uses the install
script or the manual download.

The install script is published with the website and needs nothing at release time: it finds the
newest `cli/v…` tag through GitHub's API, and downloads `zubi_<version>_<platform>.tar.gz` and
`zubi_<version>_checksums.txt` from that release. Renaming the archives, their platform names or the
checksums file breaks it, so change `site/install.sh` in the same commit. The test gate runs
`shellcheck` on it.

## One-time setup

Five secrets, shared by both release workflows, set as in [signing.md §2](signing.md#2-give-the-release-workflow-its-secrets-once):
`DEVELOPER_ID_P12_BASE64`, `DEVELOPER_ID_P12_PASSWORD`, `NOTARY_API_KEY_BASE64`, `NOTARY_API_KEY_ID`
and `NOTARY_API_ISSUER_ID`. Without them the workflow stops at the step that needs them.

The certificate and notary key Blitz releases with work here unchanged: both belong to the team, not
to an app.

## Notarized

A downloaded copy opens on first launch after macOS's usual "downloaded from the internet"
confirmation, with no "unverified developer" block and no `xattr` step. An update keeps the user's
permission grants, because the requirement they match pins the team rather than the certificate.
See [signing.md](signing.md#what-the-updater-trusts).

## Pull requests

`tests.yml` runs on every pull request, so drift between the server, the spec and the CLI shows up
there. It does not sign or exercise the app: run it locally and use what you changed before you open
one; see [CONTRIBUTING.md](../CONTRIBUTING.md).
