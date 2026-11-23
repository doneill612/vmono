#!/bin/sh
# vmono installer for macOS and Linux.
#
#   curl -fsSL https://github.com/doneill612/vmono/releases/latest/download/install.sh | sh
#   curl -fsSL https://github.com/doneill612/vmono/releases/latest/download/install.sh | sh -s v0.1.0
#
# Downloads the release archive for this OS and CPU, verifies it against the
# release's checksums.txt, and installs the vmono binary.
#
# Environment:
#   VMONO_VERSION        release tag to install, e.g. v0.1.0 (default: latest)
#   VMONO_INSTALL_DIR    where to put the binary (default: $HOME/.local/bin)
#   VMONO_DOWNLOAD_BASE  releases URL to download from
#                        (default: https://github.com/doneill612/vmono/releases)

set -eu

# say prints a progress line to stderr.
say() {
	printf 'vmono-install: %s\n' "$*" >&2
}

# die prints an error to stderr and exits.
die() {
	printf 'vmono-install: error: %s\n' "$*" >&2
	exit 1
}

# have reports whether a command is available.
have() {
	command -v "$1" >/dev/null 2>&1
}

# fetch downloads URL $1 to file $2 with curl, or wget when curl is missing.
fetch() {
	if have curl; then
		curl -fsSL --retry 3 -o "$2" "$1" || die "download failed: $1"
	elif have wget; then
		wget -q -O "$2" "$1" || die "download failed: $1"
	else
		die "need curl or wget"
	fi
}

# sha256_of prints the SHA-256 of file $1.
sha256_of() {
	if have sha256sum; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif have shasum; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "need sha256sum or shasum to verify the download"
	fi
}

# detect_os prints the release OS name for this machine.
detect_os() {
	case "$(uname -s)" in
	Darwin) echo darwin ;;
	Linux) echo linux ;;
	*) die "unsupported OS $(uname -s); on Windows use install.ps1" ;;
	esac
}

# detect_arch prints the release architecture name for this machine.
detect_arch() {
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	arm64 | aarch64) echo arm64 ;;
	*) die "unsupported architecture $(uname -m)" ;;
	esac
}

# main installs vmono. Everything runs from this function, called on the last
# line, so a download cut off partway through never executes half a script.
main() {
	version="${1:-${VMONO_VERSION:-latest}}"
	install_dir="${VMONO_INSTALL_DIR:-$HOME/.local/bin}"
	base="${VMONO_DOWNLOAD_BASE:-https://github.com/doneill612/vmono/releases}"
	base="${base%/}"

	os="$(detect_os)"
	arch="$(detect_arch)"
	archive="vmono_${os}_${arch}.tar.gz"

	if [ "$version" = latest ]; then
		url_dir="$base/latest/download"
	else
		url_dir="$base/download/$version"
	fi

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "downloading $archive ($version)"
	fetch "$url_dir/$archive" "$tmp/$archive"
	fetch "$url_dir/checksums.txt" "$tmp/checksums.txt"

	want="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
	[ -n "$want" ] || die "$archive is not listed in checksums.txt"
	got="$(sha256_of "$tmp/$archive")"
	[ "$want" = "$got" ] || die "checksum mismatch for $archive (expected $want, got $got)"

	tar -xzf "$tmp/$archive" -C "$tmp" vmono || die "could not extract $archive"
	mkdir -p "$install_dir"
	install -m 0755 "$tmp/vmono" "$install_dir/vmono"
	if [ "$os" = darwin ]; then
		xattr -d com.apple.quarantine "$install_dir/vmono" 2>/dev/null || true
	fi

	say "installed $("$install_dir/vmono" --version) to $install_dir/vmono"
	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) say "note: $install_dir is not on your PATH; add it to your shell profile" ;;
	esac
	have git || say "note: git 2.30 or newer is required and was not found"
}

main "$@"
