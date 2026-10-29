# Release

How a build reaches a user. The local development loop is in [development.md](development.md);
the signing identities are in [signing.md](signing.md).

## Every push to `main` is a release

`.github/workflows/release.yml` runs on every push to `main` that touches more than Markdown
(`**/*.md`) or `docs/`. One job on a `macos-26` runner, with Xcode 26:

1. **Version.** `MAJOR.MINOR` comes from `MARKETING_VERSION` in `project.yml`; the patch is the
   workflow's run number, so each release is strictly newer than the last and the updater offers
   it. `CURRENT_PROJECT_VERSION` is the run number too. Bump `MAJOR.MINOR` in `project.yml` when a
   release deserves it, then regenerate the project.
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
7. **Publish** a GitHub Release `v<version>` with that zip and GitHub's generated changelog, marked
   `--latest`.

Releases run one at a time (`concurrency: release`), and a queued one is never cancelled.

The in-app updater reads those releases directly; see [features/updates.md](features/updates.md).
There is one channel: no beta, no DMG, no Homebrew cask.

## Docs-only pushes

A push that changes only Markdown or `docs/` does not release, so a doc fix can land on `main` on its
own without shipping a build that is identical to the last one.

## One-time setup

Five secrets, set as in [signing.md §2](signing.md#2-give-the-release-workflow-its-secrets-once):
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

The release workflow is the repository's only one: nothing runs on a pull request. Build the app
locally and exercise what you changed before you open one; see [CONTRIBUTING.md](../CONTRIBUTING.md).
