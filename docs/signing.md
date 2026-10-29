# Signing

Zubi is always signed with a **stable identity**. Keeping the _same_ identity on every build is what
makes macOS remember the Reminders, Calendar and Automation (Notes) permissions across rebuilds and
updates. Ad-hoc signing changes every build, and macOS forgets the grants each time. There are two:

- **local builds** (§1) sign with a self-signed `Zubi Self-Signed` identity in your login keychain,
  and
- **releases** (§2) sign with the team's **Developer ID Application** certificate (team
  `HS26J3YA63`) and are notarized and stapled. The certificate and the notary API key live only in
  GitHub secrets the release workflow reads.

What the updater trusts is [below](#what-the-updater-trusts).

## 1. Create the `Zubi Self-Signed` identity (once)

Run these in a terminal. They generate a self-signed code-signing certificate and import it into your
login keychain. They call `/usr/bin/openssl` (LibreSSL) by path on purpose: a Homebrew OpenSSL 3
earlier on `PATH` writes a `.p12` whose MAC `security import` rejects as "wrong password?".

```sh
# Generate a self-signed code-signing cert (10-year, codeSigning use).
/usr/bin/openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -keyout /tmp/zubi-key.pem -out /tmp/zubi-cert.pem \
  -subj "/CN=Zubi Self-Signed" \
  -addext "basicConstraints=critical,CA:false" \
  -addext "keyUsage=critical,digitalSignature" \
  -addext "extendedKeyUsage=critical,codeSigning"

# Bundle it as a .p12 (the non-empty password keeps `security import` happy).
/usr/bin/openssl pkcs12 -export -inkey /tmp/zubi-key.pem -in /tmp/zubi-cert.pem \
  -name "Zubi Self-Signed" -out /tmp/zubi.p12 -passout pass:zubi

# Import into the login keychain so codesign can use it without prompting.
security import /tmp/zubi.p12 -k ~/Library/Keychains/login.keychain-db \
  -P zubi -A -T /usr/bin/codesign

rm -f /tmp/zubi-key.pem /tmp/zubi-cert.pem /tmp/zubi.p12
```

Verify it's there:

```sh
security find-identity -p codesigning | grep "Zubi Self-Signed"
```

`project.yml` names it in `CODE_SIGN_IDENTITY`, so local builds (Xcode and `xcodebuild`) sign with
it, and you grant each permission once.

## 2. Give the release workflow its secrets (once)

Five GitHub secrets, set with `gh` (authed with admin on the repo). Without them the workflow stops
at the step that needs them. The certificate and notary key Blitz uses work here as they are; if you
still have the files, skip to step 3.

1. **The Developer ID certificate.** In Keychain Access, under *login › My Certificates*, export
   `Developer ID Application: Sascha Krug (HS26J3YA63)`, certificate and private key together, as
   a `.p12` with a password.
2. **The notary API key.** In App Store Connect, *Users and Access › Integrations › App Store
   Connect API › Team Keys*, generate a key with **Developer** access and download its
   `AuthKey_<key-id>.p8` (Apple offers the download once). The issuer ID is shown above the key list.
3. **Upload them.** The script imports the `.p12` the way the release job does, asks Apple's notary
   service to accept the key, and only then sets all five secrets on `fa-krug/zubi`:

   ```sh
   ./Scripts/set-release-secrets.sh DeveloperID.p12 AuthKey_<key-id>.p8 <issuer-id>
   ```

Delete both files afterwards, or keep them only in a password manager.

**Renewing the certificate** is a fresh export and another run of the script. The updater and users'
grants pin the team, not the certificate, so nothing else changes. An expired certificate fails the
release at the build step; builds it already signed stay valid, because they carry a secure
timestamp.

## Hardened runtime

**Release only**: `ENABLE_HARDENED_RUNTIME: YES`, which notarization requires. Debug must stay
without it. Hardened runtime turns on library validation, and Xcode's `Zubi Dev.debug.dylib` is
refused at launch because a self-signed identity carries no Team ID for the loader to match. The flag
is not part of the designated requirement, so turning it on costs no grant.

Zubi is not sandboxed and has no helper binary. Each entitlement in `Sources/Zubi.entitlements` earns
its place:

| Entitlement | Without it |
| --- | --- |
| `com.apple.security.personal-information.calendars` | `requestFullAccessToReminders()` and `requestFullAccessToEvents()` return `false` in milliseconds with no dialog, and Zubi never appears under System Settings › Reminders or Calendars. The one entitlement covers both prompts |
| `com.apple.security.automation.apple-events` | Every Apple event `osascript` sends Notes on Zubi's behalf is refused with `-1743` and no prompt, so Notes can never be connected |

**A usage string is not enough under the hardened runtime.** `tccd` checks the matching entitlement
*before* it prompts, and without it logs "requires entitlement … but it is missing" and denies on the
spot: no dialog, no error, status still `.notDetermined`. An existing grant keeps working, since
`tccd` does not re-check it, so a missing entitlement surfaces only on a fresh install. Adding a
protected resource therefore means adding its usage string to `Sources/Info.plist` *and* its
entitlement.

`RESOURCE_ENTITLEMENTS` in `Scripts/verify-signature.sh` maps protected resources' usage strings to
their entitlements, including resources Zubi does not use. That grants nothing; only
`Zubi.entitlements` does, and a row whose usage string `Info.plist` doesn't declare is skipped. It is
there so a future feature that adds the usage string but forgets the entitlement fails the release
instead of shipping a prompt that can never appear.

If Zubi is ever sandboxed, it also needs `com.apple.security.network.server` for the HTTP API, and
reading Notes through `osascript` would need rethinking.

`./Scripts/verify-signature.sh <path-to-.app>` asserts all of this: the runtime flag on
`Contents/MacOS/Zubi`, an intact seal, no `get-task-allow`, and an entitlement for every usage string
`Info.plist` declares. The release job runs it before notarizing.

## What the updater trusts

`BundleSignature` accepts a staged bundle only when its seal validates, nested code included, and it
satisfies the Developer ID requirement for team `HS26J3YA63`. The release workflow checks the built
app against that same string, read straight from `BundleSignature.swift`, before notarizing.

The requirement pins the team rather than the certificate, so a Developer ID renewal strands nobody.
It deliberately omits the `notarized` keyword. That resolves a ticket through `syspolicyd` or the
network, and the updater verifies in a cache directory Gatekeeper has never assessed, so an offline
Mac would refuse a bundle the chain already proves is ours.

Users' TCC grants (Reminders, Calendar, Automation) are tied to the app's designated requirement,
which names the same team, so certificate renewals and updates keep them.

## Quarantine (separate from signing)

macOS quarantines anything downloaded from the internet. A release is notarized with its ticket
stapled into the bundle, so Gatekeeper opens a quarantined copy after its usual one-time
confirmation, with no "unverified developer" block and no network lookup.

Updates never touch it: an archive Zubi downloads itself is not quarantined.
