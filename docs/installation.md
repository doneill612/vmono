# Installation

[← vmono](../README.md)

## Install script

macOS and Linux:

```sh
curl -fsSL https://github.com/doneill612/vmono/releases/latest/download/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://github.com/doneill612/vmono/releases/latest/download/install.ps1 | iex
```

The script downloads the archive for your OS and CPU from the latest [release](https://github.com/doneill612/vmono/releases), checks it against the release's `checksums.txt`, and installs `vmono` to `~/.local/bin` (`%LOCALAPPDATA%\Programs\vmono\bin` on Windows, which it adds to your user `PATH`). On macOS and Linux, make sure `~/.local/bin` is on your `PATH`. Requires git 2.30 or newer.

| Variable | Does |
| --- | --- |
| `VMONO_VERSION` | Install a specific release, e.g. `v0.1.0`, instead of the latest. On macOS and Linux you can also pass it as an argument: `... \| sh -s v0.1.0` |
| `VMONO_INSTALL_DIR` | Install somewhere else |
| `VMONO_DOWNLOAD_BASE` | Download from a mirror of the releases page |

To check what you have, run `vmono --version`.

Releases from `v0.1.0` are immutable, and GitHub signs an attestation of their files. To check an archive you downloaded yourself, use the [GitHub CLI](https://cli.github.com):

```sh
gh release verify-asset v0.1.0 vmono_linux_amd64.tar.gz --repo doneill612/vmono
```

A tarball downloaded in a browser on macOS is quarantined by Gatekeeper; clear it with `xattr -d com.apple.quarantine vmono`. The install script doesn't need this.

## From source

Requires [Go](https://go.dev/dl/) 1.25 or newer and git 2.30 or newer. Every route below installs the same binary; pick the one for your platform.

**macOS and Linux** (and WSL), with `make`:

```sh
git clone https://github.com/doneill612/vmono
cd vmono
make install
```

This builds the binary and copies it to `~/.local/bin/vmono`. Override the location with `PREFIX=` or `BINDIR=`.

**Windows**, in PowerShell. `make` is not normally installed on Windows, so these are the same steps by hand:

```powershell
git clone https://github.com/doneill612/vmono
cd vmono

# Build, stamped with the git version
$version = git describe --tags --always --dirty 2>$null; if (-not $version) { $version = 'dev' }
go build -trimpath -ldflags "-X github.com/doneill612/vmono/internal/buildinfo.version=$version" -o bin\vmono.exe .\cmd\vmono

# Install to the same folder as the install script, and add it to your user PATH
$dir = Join-Path $env:LOCALAPPDATA 'Programs\vmono\bin'
New-Item -ItemType Directory -Force $dir | Out-Null
Copy-Item bin\vmono.exe $dir -Force
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
    $newPath = (@($userPath, $dir) | Where-Object { $_ }) -join ';'
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
}
```

This installs to `%LOCALAPPDATA%\Programs\vmono\bin`, the same place as the install script, so either one can replace the other. Open a new terminal afterwards so the `PATH` change takes effect.

**Any OS, without cloning**, with `go install`:

```sh
go install github.com/doneill612/vmono/cmd/vmono@latest
```

Go puts the binary in `$(go env GOPATH)/bin` (`%USERPROFILE%\go\bin` on Windows), which must be on your `PATH`. The version is read from the module, so `vmono --version` reports the tag.

The other Makefile tasks, and their PowerShell equivalents, are in [Development](development.md).

## Uninstalling

Delete the `vmono` binary. On macOS and Linux, `make uninstall` from a clone of the repo does the same for a source install.

On Windows, remove the binary and its folder from your user `PATH`, in PowerShell:

```powershell
$dir = Join-Path $env:LOCALAPPDATA 'Programs\vmono\bin'
Remove-Item (Join-Path $dir 'vmono.exe') -ErrorAction SilentlyContinue
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$newPath = (($userPath -split ';') | Where-Object { $_ -and $_ -ne $dir }) -join ';'
[Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
```

If you installed somewhere else with `VMONO_INSTALL_DIR`, set `$dir` to that folder first.
