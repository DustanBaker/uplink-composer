// Package buildinfo carries version identity stamped at build time.
package buildinfo

// Version is overridden at release time via
// -ldflags "-X github.com/DustanBaker/uplink-composer/internal/buildinfo.Version=v0.x.y".
var Version = "v0.1.1-dev"

// UserAgent identifies The Uplink CompOSer in outbound HTTP requests.
func UserAgent() string { return "uplink-composer/" + Version }
