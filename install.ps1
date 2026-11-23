# vmono installer for Windows.
#
#   irm https://github.com/doneill612/vmono/releases/latest/download/install.ps1 | iex
#
# Downloads the release archive for this CPU, verifies it against the
# release's checksums.txt, installs vmono.exe, and adds its folder to the
# user PATH.
#
# Environment:
#   VMONO_VERSION        release tag to install, e.g. v0.1.0 (default: latest)
#   VMONO_INSTALL_DIR    where to put vmono.exe
#                        (default: %LOCALAPPDATA%\Programs\vmono\bin)
#   VMONO_DOWNLOAD_BASE  releases URL to download from
#                        (default: https://github.com/doneill612/vmono/releases)

# Install-Vmono installs vmono. Everything runs inside this function, called on
# the last line, so a download cut off partway through never executes half a
# script.
function Install-Vmono {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    $version = if ($env:VMONO_VERSION) { $env:VMONO_VERSION } else { 'latest' }
    $installDir = if ($env:VMONO_INSTALL_DIR) { $env:VMONO_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\vmono\bin' }
    $base = if ($env:VMONO_DOWNLOAD_BASE) { $env:VMONO_DOWNLOAD_BASE } else { 'https://github.com/doneill612/vmono/releases' }
    $base = $base.TrimEnd('/')

    $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "unsupported architecture $($env:PROCESSOR_ARCHITECTURE)" }
    }
    $archive = "vmono_windows_$arch.zip"
    $urlDir = if ($version -eq 'latest') { "$base/latest/download" } else { "$base/download/$version" }

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("vmono-install-" + [System.Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "vmono-install: downloading $archive ($version)"
        Invoke-WebRequest -UseBasicParsing -Uri "$urlDir/$archive" -OutFile (Join-Path $tmp $archive)
        Invoke-WebRequest -UseBasicParsing -Uri "$urlDir/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

        $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -eq $archive } | Select-Object -First 1
        if (-not $line) { throw "$archive is not listed in checksums.txt" }
        $want = ($line -split '\s+')[0].ToLower()
        $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $archive)).Hash.ToLower()
        if ($want -ne $got) { throw "checksum mismatch for $archive (expected $want, got $got)" }

        Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x') -Force
        New-Item -ItemType Directory -Path $installDir -Force | Out-Null
        Copy-Item (Join-Path $tmp 'x\vmono.exe') (Join-Path $installDir 'vmono.exe') -Force
    }
    finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }

    $exe = Join-Path $installDir 'vmono.exe'
    Write-Host "vmono-install: installed $(& $exe --version) to $exe"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = if ($userPath) { $userPath -split ';' } else { @() }
    if ($entries -notcontains $installDir) {
        $newPath = if ($userPath) { "$userPath;$installDir" } else { $installDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Host "vmono-install: added $installDir to your PATH; open a new terminal to use vmono"
    }
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
        Write-Host 'vmono-install: note: git 2.30 or newer is required and was not found'
    }
}

Install-Vmono
