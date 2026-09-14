package library

import (
	"os"
	"path/filepath"
	"testing"
)

// Removing one entry keeps a blob another entry still points at.
func TestRemoveKeepsSharedBlob(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sha := "abababababababababababababababababababababababababababababababab"
	os.MkdirAll(filepath.Dir(l.BlobPath(sha)), 0o755)
	os.WriteFile(l.BlobPath(sha), []byte("12345"), 0o644)
	for _, id := range []string{"a", "b"} {
		if err := l.record(Entry{ID: id, SHA256: sha, Size: 5}); err != nil {
			t.Fatal(err)
		}
	}
	if freed, err := l.Remove("a"); err != nil || freed != 0 {
		t.Fatalf("remove a: %d %v", freed, err)
	}
	if _, err := l.Resolve("b"); err != nil {
		t.Fatalf("b lost its blob: %v", err)
	}
	if freed, err := l.Remove("b"); err != nil || freed != 5 {
		t.Fatalf("remove b: %d %v", freed, err)
	}
	if _, err := os.Stat(l.BlobPath(sha)); !os.IsNotExist(err) {
		t.Error("blob left after its last entry was removed")
	}
	if _, err := l.Remove("b"); err == nil {
		t.Error("removing a missing entry succeeded")
	}
}
