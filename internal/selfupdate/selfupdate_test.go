package selfupdate

import (
	"context"
	"testing"
)

// TestCompareVersions pins the ordering that decides whether an update is
// offered. The dev-build case is the one that matters day to day: a local
// build must see its own release as newer, or `uplink update` would tell
// everyone running a dev binary that they are current.
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.2.1", "v0.2.0", 1},
		{"v0.2.0", "v0.2.1", -1},
		{"v0.2.0", "v0.2.0", 0},
		{"v0.10.0", "v0.9.0", 1},  // numeric, not lexical
		{"v1.0.0", "v0.99.99", 1}, // major wins
		{"v0.2.0", "v0.2.0-dev", 1},
		{"v0.2.0-dev", "v0.2.0", -1},
		{"v0.2.0", "v0.1.1-dev", 1}, // the shipped default
		{"v0.2", "v0.2.0", 0},       // missing components are zero
		{"0.3.0", "v0.2.0", 1},      // a missing v does not change the order
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestAssetName matches the names the release workflow actually publishes.
func TestAssetName(t *testing.T) {
	cases := []struct{ prog, os, arch, want string }{
		{"uplink", "windows", "amd64", "uplink-v0.2.0-windows-amd64.exe"},
		{"uplink", "linux", "arm64", "uplink-v0.2.0-linux-arm64"},
		{"uplink", "darwin", "arm64", "uplink-v0.2.0-darwin-arm64"},
		{"uplink-app", "windows", "amd64", "uplink-app-v0.2.0-windows-amd64.exe"},
	}
	for _, c := range cases {
		if got := AssetName(c.prog, "v0.2.0", c.os, c.arch); got != c.want {
			t.Errorf("AssetName(%s, %s/%s) = %q, want %q", c.prog, c.os, c.arch, got, c.want)
		}
	}
}

// TestCheckLive confirms the published release really carries an asset for
// this platform and a checksum file — the two things Check refuses without.
// Network, so it is skipped under -short (which is what CI runs).
func TestCheckLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	rel, err := Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rel.Version == "" || rel.URL == "" || rel.SumsURL == "" {
		t.Fatalf("incomplete release: %+v", rel)
	}
	t.Logf("latest %s, asset %s (%d bytes), newer than this build: %v",
		rel.Version, rel.Asset, rel.Size, rel.Newer)
}
