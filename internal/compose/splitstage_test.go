package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/uplinkresearch/dsky/internal/fsimg"
)

// A rebuild from a cached extraction stages the extraction folder, which by
// then also holds the cached split parts. They must end up only in /sources.
func TestCachedSplitPartsStagedOnce(t *testing.T) {
	extract := t.TempDir()
	write := func(rel string, size int64) string {
		p := filepath.Join(extract, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		f, _ := os.Create(p)
		f.Truncate(size)
		f.Close()
		return p
	}
	write("sources/install.wim", fsimg.MaxFileSize+1)
	write("setup.exe", 10)
	write("dsky-swm/install.swm", 100)
	write("dsky-swm/install2.swm", 100)

	stage := fsimg.StageMap{}
	if err := stage.AddTree(extract, "/"); err != nil {
		t.Fatal(err)
	}
	if err := splitOversizeWIM(context.Background(), Request{}, stage); err != nil {
		t.Fatal(err)
	}
	for img := range stage {
		if filepath.Dir(img) == "/dsky-swm" {
			t.Errorf("cached split part staged at %s", img)
		}
	}
	for _, want := range []string{"/sources/install.swm", "/sources/install2.swm", "/setup.exe"} {
		if _, ok := stage[want]; !ok {
			t.Errorf("%s missing from the stage", want)
		}
	}
	if _, ok := stage["/sources/install.wim"]; ok {
		t.Error("the unsplit install.wim is still staged")
	}
}
