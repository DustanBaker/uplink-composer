package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/uplinkresearch/bootwright/internal/compose"
)

// imageExts are the extensions that mean "this argument is a file to write",
// not a recipe to build. The list is for disambiguation only — compression is
// sniffed from the file's magic bytes, so a .img.xz, a .iso.zst or a wrongly
// named file all still work.
var imageExts = []string{".iso", ".img", ".raw", ".bin", ".wic", ".vhd", ".dd"}

// looksLikeImagePath reports whether the word names an image file rather than
// a recipe. The extension may be followed by a compression suffix, which is
// how appliance images normally arrive.
func looksLikeImagePath(word string) bool {
	w := strings.ToLower(word)
	for _, suffix := range []string{"", ".xz", ".zst", ".zstd", ".gz", ".bz2"} {
		for _, ext := range imageExts {
			if strings.HasSuffix(w, ext+suffix) {
				return true
			}
		}
	}
	return false
}

// artifactForPath resolves a path the operator named. A missing sidecar used
// to be a hard error here; it is a normal case now, so the only real error
// left is a file that is not there at all. The portal shares this logic via
// compose.ResolveImage, so both agree about the size interlock.
func artifactForPath(path string) (*compose.Artifact, bool, error) {
	return compose.ResolveImage(path)
}

// describeForeignImage says what is about to be written and, more
// importantly, what has not been checked. A composed artifact came from
// pinned sources; a file someone points at has whatever provenance they gave
// it, and the readback verify that follows proves only that the stick holds
// what was sent to it.
func describeForeignImage(a *compose.Artifact) {
	fmt.Printf("Writing %s (%d MiB", filepath.Base(a.Path), a.Size>>20)
	if a.Compress != "" && a.Compress != "none" {
		fmt.Printf(", %s-compressed, expands on the way to the stick", a.Compress)
	}
	fmt.Println(")")
	fmt.Println("  Not from the catalog, so there is no hash to check it against —")
	fmt.Println("  verify the download yourself if the source matters.")
}
