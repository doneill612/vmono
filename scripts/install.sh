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

# setup_style sets the color variables. Color and the download progress bar
# are used only when stderr is a terminal, NO_COLOR is unset and TERM is not
# dumb, so CI logs and redirected output stay plain.
setup_style() {
	interactive=0
	mark="ok"
	accent="" ok="" warn="" err="" dim="" reset=""
	if [ -t 2 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-}" != dumb ]; then
		interactive=1
		mark="✓"
		esc="$(printf '\033')"
		accent="${esc}[1;38;5;214m" # amber, matching the vmono TUI
		ok="${esc}[32m"
		warn="${esc}[33m"
		err="${esc}[31m"
		dim="${esc}[2m"
		reset="${esc}[0m"
	fi
}

# say prints a progress step to stderr.
say() {
	printf '%s==>%s %s\n' "$accent" "$reset" "$*" >&2
}

# note prints a non-fatal warning to stderr.
note() {
	printf '%snote:%s %s\n' "$warn" "$reset" "$*" >&2
}

# die prints an error to stderr and exits.
die() {
	printf '%serror:%s %s\n' "$err" "$reset" "$*" >&2
	exit 1
}

# have reports whether a command is available.
have() {
	command -v "$1" >/dev/null 2>&1
}

# fetch downloads URL $1 to file $2 with curl, or wget when curl is missing.
# A third argument of "progress" shows a progress bar when interactive.
fetch() {
	bar=0
	[ "${3:-}" = progress ] && [ "$interactive" = 1 ] && bar=1
	if have curl; then
		if [ "$bar" = 1 ]; then
			curl -fL# --retry 3 -o "$2" "$1" || die "download failed: $1"
		else
			curl -fsSL --retry 3 -o "$2" "$1" || die "download failed: $1"
		fi
	elif have wget; then
		if [ "$bar" = 1 ] && wget --help 2>&1 | grep -q show-progress; then
			wget -q --show-progress -O "$2" "$1" || die "download failed: $1"
		else
			wget -q -O "$2" "$1" || die "download failed: $1"
		fi
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
	setup_style
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

	say "detected $os/$arch"
	say "downloading $archive ${dim}($version)${reset}"
	fetch "$url_dir/$archive" "$tmp/$archive" progress
	fetch "$url_dir/checksums.txt" "$tmp/checksums.txt"

	say "verifying checksum"
	want="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
	[ -n "$want" ] || die "$archive is not listed in checksums.txt"
	got="$(sha256_of "$tmp/$archive")"
	[ "$want" = "$got" ] || die "checksum mismatch for $archive (expected $want, got $got)"

	say "installing to $install_dir"
	tar -xzf "$tmp/$archive" -C "$tmp" vmono || die "could not extract $archive"
	mkdir -p "$install_dir"
	install -m 0755 "$tmp/vmono" "$install_dir/vmono"
	if [ "$os" = darwin ]; then
		xattr -d com.apple.quarantine "$install_dir/vmono" 2>/dev/null || true
	fi

	installed="$("$install_dir/vmono" --version)" # "vmono (0.1.0)"
	installed="${installed##* }"
	installed="${installed#(}"
	installed="${installed%)}"
	printf '%s%s%s installed vmono %s%s%s %s(%s/vmono)%s\n' \
		"$ok" "$mark" "$reset" "$accent" "$installed" "$reset" "$dim" "$install_dir" "$reset" >&2
	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*) note "$install_dir is not on your PATH; add it to your shell profile" ;;
	esac
	have git || note "git 2.30 or newer is required and was not found"
}

main "$@"
