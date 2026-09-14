package helpers

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/uplinkresearch/dsky/internal/manifest"
)

// Fido refuses to run anywhere but Windows. Off Windows the error has to say
// what to do instead, not relay whatever PowerShell printed first.
func TestFidoOffWindowsSaysWhatToDo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Fido runs here")
	}
	_, err := ResolveFidoURL(context.Background(), t.TempDir(), &manifest.FidoSpec{Win: "10"})
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"software-download/windows10ISO", "Use an ISO you downloaded", "--iso"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}
