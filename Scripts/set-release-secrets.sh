#!/bin/bash
# Check, then upload, the five secrets release.yml signs and notarizes with. See docs/signing.md §2.
# Usage: set-release-secrets.sh <DeveloperID.p12> <AuthKey_<key-id>.p8> <issuer-id>
set -uo pipefail

REPO=fa-krug/zubi
TEAM=HS26J3YA63
P12="${1:?usage: set-release-secrets.sh <DeveloperID.p12> <AuthKey_<key-id>.p8> <issuer-id>}"
P8="${2:?usage: set-release-secrets.sh <DeveloperID.p12> <AuthKey_<key-id>.p8> <issuer-id>}"
ISSUER="${3:?usage: set-release-secrets.sh <DeveloperID.p12> <AuthKey_<key-id>.p8> <issuer-id>}"

die() {
    echo "✗ $1" >&2
    exit 1
}

[ -f "$P12" ] || die "$P12 does not exist"
[ -f "$P8" ] || die "$P8 does not exist"
# Apple names the download after the key and offers it once, so the name is the record.
KEY_ID="$(basename "$P8" .p8)"
KEY_ID="${KEY_ID#AuthKey_}"
[[ "$KEY_ID" =~ ^[A-Z0-9]{10}$ ]] || die "expected the file Apple named AuthKey_<key-id>.p8, got $(basename "$P8")"
[[ "$ISSUER" =~ ^[0-9a-fA-F-]{36}$ ]] || die "the issuer ID is a UUID, shown above the key list"
grep -q 'BEGIN PRIVATE KEY' "$P8" || die "$P8 is not a private key"

read -rsp "Password you gave the .p12 export: " P12_PASSWORD
echo

# The same import the release job runs, so a .p12 that passes here imports there.
SCRATCH="$(mktemp -d)"
trap 'security delete-keychain "$SCRATCH/check.keychain-db" 2>/dev/null; rm -rf "$SCRATCH"' EXIT
security create-keychain -p check "$SCRATCH/check.keychain-db" || die "could not create a scratch keychain"
security import "$P12" -k "$SCRATCH/check.keychain-db" -P "$P12_PASSWORD" -T /usr/bin/codesign >/dev/null ||
    die "the .p12 did not import — wrong password, or it was exported without its private key"
security find-identity -p codesigning "$SCRATCH/check.keychain-db" |
    grep -q "Developer ID Application: .*($TEAM)" ||
    die "the .p12 holds no Developer ID Application identity for team $TEAM"
echo "✓ .p12 holds the Developer ID Application identity for $TEAM"

xcrun notarytool history --key "$P8" --key-id "$KEY_ID" --issuer "$ISSUER" >/dev/null ||
    die "Apple's notary service refused the API key — check the key ID, issuer ID and its access"
echo "✓ the notary service accepts API key $KEY_ID"

base64 < "$P12" | tr -d '\n' | gh secret set DEVELOPER_ID_P12_BASE64 --repo "$REPO" || exit 1
printf '%s' "$P12_PASSWORD" | gh secret set DEVELOPER_ID_P12_PASSWORD --repo "$REPO" || exit 1
base64 < "$P8" | tr -d '\n' | gh secret set NOTARY_API_KEY_BASE64 --repo "$REPO" || exit 1
printf '%s' "$KEY_ID" | gh secret set NOTARY_API_KEY_ID --repo "$REPO" || exit 1
printf '%s' "$ISSUER" | gh secret set NOTARY_API_ISSUER_ID --repo "$REPO" || exit 1
echo "✓ all five secrets are set on $REPO"
