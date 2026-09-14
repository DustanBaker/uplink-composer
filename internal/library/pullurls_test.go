package library

import (
	"testing"

	"github.com/uplinkresearch/dsky/internal/manifest"
)

// Mirrors only for a pinned file: the hash is what catches a mirror serving
// something else, so an unpinned or provider-resolved source never uses one.
func TestPullURLs(t *testing.T) {
	u := "https://releases.ubuntu.com/26.04/ubuntu-26.04.1-desktop-amd64.iso"
	if got := pullURLs(&manifest.Source{SHA256: "abc"}, u); len(got) < 2 || got[0] != u {
		t.Fatalf("pinned: %v", got)
	}
	if got := pullURLs(&manifest.Source{}, u); len(got) != 1 {
		t.Fatalf("unpinned used mirrors: %v", got)
	}
	if got := pullURLs(&manifest.Source{SHA256: "abc", Provider: "fido"}, u); len(got) != 1 {
		t.Fatalf("provider used mirrors: %v", got)
	}
	if got := pullURLs(&manifest.Source{SHA256: "abc"}, "https://example.com/a.iso"); len(got) != 1 {
		t.Fatalf("no mirrors known, yet got %v", got)
	}
}
