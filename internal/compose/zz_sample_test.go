package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/uplinkresearch/bootwright/internal/recipe"
	"github.com/uplinkresearch/bootwright/internal/workspace"
)

// TestWriteSampleAnswerFiles writes the two answer files a real build
// produces, so they can be opened in Windows System Image Manager and checked
// against the actual unattend schema. That is the one verification that needs
// no domain controller and no hardware, and it is Microsoft's own first
// recommendation for authoring answer files.
//
// Skipped unless SAMPLE_OUT is set: this is a tool, not a test.
func TestWriteSampleAnswerFiles(t *testing.T) {
	out := os.Getenv("SAMPLE_OUT")
	if out == "" {
		t.Skip("set SAMPLE_OUT to write the sample answer files")
	}

	write(t, filepath.Join(out, "autounattend-credentialed-join.xml"),
		renderScaffolded(t, &recipe.DomainSpec{
			Join:     "corp.example.com",
			Username: `CORP\svc-domainjoin`,
			Password: "${var:pw}",
			OU:       "OU=Workstations,DC=corp,DC=example,DC=com",
		}))

	ws := filepath.Join(t.TempDir(), "ws")
	if err := workspace.Scaffold(ws, "Example Org"); err != nil {
		t.Fatal(err)
	}
	blob := "QkFTRTY0LUJMT0ItRlJPTS1kam9pbi1wcm92aXNpb24="
	if err := writeBytes(filepath.Join(ws, "payload", "pc01.odj"), utf16le(blob, true)); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(out, "autounattend-offline-join.xml"),
		renderIn(t, ws, &recipe.DomainSpec{Blob: "payload/pc01.odj"}))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}
