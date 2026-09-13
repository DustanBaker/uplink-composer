package library

import (
	"os"
	"path/filepath"
	"testing"
)

// seed makes a directory that looks like a library with something in it, so a
// migration that silently starts fresh is distinguishable from one that moved
// the real thing.
func seed(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blobs", "iso"), []byte("a downloaded ISO"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasSeed(t *testing.T, dir string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "blobs", "iso"))
	return err == nil && string(b) == "a downloaded ISO"
}

// TestMigratesFromEveryFormerName is the whole point of the list. The tool has
// been renamed twice, and a cache is tens of gigabytes of ISOs — losing it
// looks like the tool forgetting everything and re-downloading in silence.
//
// The regression this guards against is real: renaming the tool rewrote the
// destination name and left the source list pointing at the name from two
// renames ago, which would have orphaned every current library.
func TestMigratesFromEveryFormerName(t *testing.T) {
	for _, old := range formerNames {
		t.Run(old, func(t *testing.T) {
			base := t.TempDir()
			seed(t, filepath.Join(base, old))

			root := filepath.Join(base, "bootwright")
			if _, err := Open(root); err != nil {
				t.Fatal(err)
			}
			if !hasSeed(t, root) {
				t.Errorf("a library at %q was not carried over — it would look like a fresh install", old)
			}
			if _, err := os.Stat(filepath.Join(base, old)); !os.IsNotExist(err) {
				t.Errorf("the old %q directory is still there; it was copied rather than moved", old)
			}
		})
	}
}

// TestKeepsAnExistingLibrary: migration must never run over a library that is
// already in place, or a stale directory from an old version would overwrite
// the current one.
func TestKeepsAnExistingLibrary(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "bootwright")
	seed(t, root)
	if err := os.MkdirAll(filepath.Join(base, formerNames[0], "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, formerNames[0], "blobs", "iso"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	if !hasSeed(t, root) {
		t.Error("an existing library was overwritten by a leftover directory from an older name")
	}
}

// TestOpensCleanWithNothingToMigrate: the ordinary first run.
func TestOpensCleanWithNothingToMigrate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bootwright")
	l, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if l.Root != root {
		t.Errorf("Root = %q, want %q", l.Root, root)
	}
	if _, err := os.Stat(l.blobDir()); err != nil {
		t.Errorf("layout not created: %v", err)
	}
}
