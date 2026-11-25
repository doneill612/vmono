# Development

[← vmono](../README.md)

```sh
go vet ./...
go test ./...
```

Integration tests build throwaway source repos with a bare remote in a temp directory and run real create, edit, sync, migrate and delete cycles. They never touch your config, state or git identity. CI runs on Linux, macOS and Windows.

## Build tasks

On macOS and Linux, with `make`:

```sh
make build      # build to bin/vmono
make test       # go vet, then go test
make install    # build and copy to ~/.local/bin (see Installation)
make uninstall  # remove the installed binary
make clean      # remove bin/ and dist/
make snapshot   # dry-run the release build (needs GoReleaser)
```

The same tasks on Windows, in PowerShell from the repo root:

```powershell
# Build to bin\vmono.exe (make build)
$version = git describe --tags --always --dirty 2>$null; if (-not $version) { $version = 'dev' }
go build -trimpath -ldflags "-X github.com/doneill612/vmono/internal/buildinfo.version=$version" -o bin\vmono.exe .\cmd\vmono

# Test (make test)
go vet ./...
go test ./...

# Clean (make clean)
Remove-Item -Recurse -Force bin, dist -ErrorAction SilentlyContinue

# Dry-run the release build (make snapshot); needs GoReleaser
goreleaser release --snapshot --clean
```

For installing and uninstalling by hand on Windows, see [Installation](installation.md). [GoReleaser](https://goreleaser.com) is only needed for the dry run.

## Contributing changes

Changes reach `main` through a pull request, and `main` can't be force-pushed or deleted. Admins can bypass the pull request requirement.

The workflows pin every action to a commit SHA, with the version in a comment beside it. Pin new actions the same way. Dependabot opens weekly pull requests to update those pins and the Go modules.

Report vulnerabilities privately, as described in the [security policy](../SECURITY.md).

## Releasing

Releases are cut by an admin, the only role that can create a `v*` tag. Check that CI passed on the commit, then push the tag. The release workflow runs the tests, publishes the release with GoReleaser, then installs it on Linux, macOS and Windows with the install scripts:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

A tag with a suffix, such as `v0.2.0-rc.1`, is published as a prerelease, which `latest` skips.

A release is final:

- A `v*` tag can't be moved or deleted.
- A published release is immutable: its tag and files can't be changed.
- The install checks run after publishing. If they fail, fix the problem and release the next version.

If the workflow fails before publishing for a passing reason, such as a network error, delete any draft release it left and re-run it. If the fix needs a new commit, tag the next version.
