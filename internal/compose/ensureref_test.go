package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/library"
	"github.com/uplinkresearch/dsky/internal/workspace"
)

// A recipe records which driver packs it needs; the bytes arrive when a stick
// is built. Saving a recipe used to fetch them instead, which meant waiting on
// 1.2 GB before the recipe file existed. The other half of moving that is
// this: a build has to fetch what it finds missing, or every recipe saved the
// new way would fail to build.
func TestABuildFetchesAPackTheRecipeNamesButTheLibraryLacks(t *testing.T) {
	body := []byte("PK\x03\x04 pretend driver pack")
	sum := sha256.Sum256(body)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	lib, err := library.Open(filepath.Join(dir, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(dir, "ws")
	if err := os.MkdirAll(filepath.Join(wsDir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "workspace.yaml"),
		[]byte("version: 1\norg:\n  name: Test\n  id: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	man := "id: hp-pack\nkind: driver-pack\nformat: zip\nurl: " + srv.URL + "/hp.zip\n" +
		"sha256: " + hex.EncodeToString(sum[:]) + "\nfilename: hp.zip\n"
	if err := os.WriteFile(filepath.Join(wsDir, "manifests", "hp-pack.yaml"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Load(wsDir)
	if err != nil {
		t.Fatal(err)
	}

	var stages []string
	req := Request{Workspace: ws, Library: lib, Progress: func(stage string, done, total int64) {
		stages = append(stages, stage)
	}}
	if err := ensureRef(context.Background(), req, "hp-pack"); err != nil {
		t.Fatalf("the build could not fetch the pack: %v", err)
	}
	if hits == 0 {
		t.Error("the build did not fetch the missing pack")
	}
	if _, err := lib.Resolve("hp-pack"); err != nil {
		t.Errorf("the pack is still not in the library: %v", err)
	}
	if !strings.Contains(strings.Join(stages, " "), "downloading hp-pack") {
		t.Errorf("the download was silent; stages were %v", stages)
	}

	// Already present: fetched once, not again.
	before := hits
	if err := ensureRef(context.Background(), req, "hp-pack"); err != nil {
		t.Fatal(err)
	}
	if hits != before {
		t.Error("a pack already in the library was fetched again")
	}
}

// A pack nothing describes fails with a sentence that says what to do, rather
// than the library's bare "not found".
func TestAPackWithNoManifestSaysSo(t *testing.T) {
	dir := t.TempDir()
	lib, err := library.Open(filepath.Join(dir, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(dir, "ws")
	if err := os.MkdirAll(filepath.Join(wsDir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "workspace.yaml"),
		[]byte("version: 1\norg:\n  name: Test\n  id: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Load(wsDir)
	if err != nil {
		t.Fatal(err)
	}
	err = ensureRef(context.Background(), Request{Workspace: ws, Library: lib}, "nobody-knows")
	if err == nil {
		t.Fatal("a pack nothing describes was accepted")
	}
	if !strings.Contains(err.Error(), "no manifest says where to get it") {
		t.Errorf("the error does not explain itself: %v", err)
	}
}
