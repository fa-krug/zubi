#!/bin/sh
# Installs zubi, the command-line client for Zubi, from its newest GitHub release:
#
#   curl -fsSL https://zubi.fa-krug.de/install.sh | sh
#
# It picks the build for this machine (macOS; Linux on x86-64, 64-bit ARM, or 32-bit ARM such as
# 32-bit Raspberry Pi OS), checks it against the release's SHA-256 checksums, and puts zubi in
# /usr/local/bin, asking for sudo only when that needs it. Run it again to update.
#
#   ZUBI_VERSION=0.1.42           install that version instead of the newest
#   ZUBI_INSTALL_DIR=~/.local/bin install there instead, without sudo
#
# Plain POSIX sh, as Raspberry Pi OS runs /bin/sh as dash. Everything is in main, called on the last
# line, so a download cut short runs nothing.

set -eu

REPO="fa-krug/zubi"

say() { printf '%s\n' "$*" >&2; }
die() {
	say "zubi install: $*"
	exit 1
}

# fetch URL FILE, with curl or wget, whichever there is.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --retry 3 -o "$2" "$1" || die "could not download $1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1" || die "could not download $1"
	else
		die "this needs curl or wget"
	fi
}

# platform names the release archive for this machine, as release-cli.yml names them.
platform() {
	os=$(uname -s)
	arch=$(uname -m)
	case "$os" in
	Darwin) echo darwin_universal ;;
	Linux)
		case "$arch" in
		x86_64 | amd64) echo linux_amd64 ;;
		# Also a Pi on 32-bit Raspberry Pi OS with a 64-bit kernel: zubi is a static binary, so the
		# kernel's architecture is what counts.
		aarch64 | arm64) echo linux_arm64 ;;
		armv7l | armv8l) echo linux_armv7 ;;
		armv6l) die "this is a 32-bit ARMv6 (Raspberry Pi Zero or 1), which zubi has no build for" ;;
		*) die "zubi has no build for Linux on $arch" ;;
		esac
		;;
	*) die "zubi has no installer for $os; on Windows, use Scoop: https://zubi.fa-krug.de/docs.html#install" ;;
	esac
}

# newest prints the newest CLI version. CLI releases are tagged cli/vX.Y.Z and never marked Latest,
# which belongs to the app, so this reads the release list, newest first.
newest() {
	fetch "https://api.github.com/repos/$REPO/releases?per_page=50" "$1/releases.json"
	version=$(grep -o '"tag_name": *"cli/v[0-9][^"]*"' "$1/releases.json" | head -n 1 | sed 's/.*"cli\/v\([^"]*\)"/\1/')
	[ -n "$version" ] || die "found no zubi release on GitHub"
	echo "$version"
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

main() {
	target=$(platform)
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	version=${ZUBI_VERSION:-}
	version=${version#v}
	[ -n "$version" ] || version=$(newest "$tmp")
	dir=${ZUBI_INSTALL_DIR:-/usr/local/bin}

	if [ -x "$dir/zubi" ] && [ "$("$dir/zubi" version 2>/dev/null)" = "zubi $version" ]; then
		say "zubi $version is already installed in $dir."
		exit 0
	fi

	archive="zubi_${version}_${target}.tar.gz"
	base="https://github.com/$REPO/releases/download/cli/v$version"
	say "Downloading zubi $version for $target…"
	fetch "$base/$archive" "$tmp/$archive"
	fetch "$base/zubi_${version}_checksums.txt" "$tmp/checksums.txt"
	want=$(awk -v f="$archive" '$2 == f || $2 == "*" f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "the release has no checksum for $archive"
	[ "$(sha256 "$tmp/$archive")" = "$want" ] || die "$archive doesn't match its checksum; the download is damaged, so try again"

	tar -xzf "$tmp/$archive" -C "$tmp"
	binary="$tmp/zubi_${version}_${target}/zubi"
	[ -f "$binary" ] || die "$archive has no zubi in it"

	if { [ -d "$dir" ] && [ -w "$dir" ]; } || { [ ! -e "$dir" ] && mkdir -p "$dir" 2>/dev/null; }; then
		install -m 0755 "$binary" "$dir/zubi"
	elif command -v sudo >/dev/null 2>&1; then
		say "Installing into $dir needs sudo."
		{ sudo mkdir -p "$dir" && sudo install -m 0755 "$binary" "$dir/zubi"; } ||
			die "sudo didn't install it; run this again with ZUBI_INSTALL_DIR=\$HOME/.local/bin to install without it"
	else
		die "can't write to $dir; run it with ZUBI_INSTALL_DIR=\$HOME/.local/bin"
	fi

	say "Installed zubi $version as $dir/zubi."
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) say "$dir isn't on your PATH; add it, or run $dir/zubi." ;;
	esac
	say ""
	say "Next, run: zubi"
	say "It asks for the pairing code from Zubi on your Mac (menu bar icon › Copy Pairing Code)."
}

main "$@"
