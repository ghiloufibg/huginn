// Package buildinfo exposes the version stamped at build time.
package buildinfo

import "runtime/debug"

// Version is set with -ldflags "-X github.com/ghiloufibg/huginn/internal/buildinfo.Version=v1.2.3".
var Version = ""

// String returns the stamped version, else the module version recorded by
// `go install`, else "dev".
func String() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
