// Package buildinfo reports which version of vmono is running.
package buildinfo

import "runtime/debug"

// version is the release version, stamped at link time by GoReleaser and the
// Makefile:
//
//	-ldflags "-X github.com/doneill612/vmono/internal/buildinfo.version=v0.1.0"
//
// It stays empty for a plain `go build`.
var version = ""

// Version returns the version of this build. A value stamped at link time
// wins. Without one, the module version recorded by the Go toolchain is used,
// which is how a binary installed with `go install .../cmd/vmono@v0.1.0`
// learns its own tag. A build from a working tree has neither, and reports
// "dev".
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
