//go:build darwin

package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/ebitengine/purego/objc"
)

// The native window can only be tested with a desktop session, on the main
// thread, so it runs from TestMain rather than as an ordinary test, and only
// when asked (DSKY_MAC_WINDOW_TEST=1, set by .github/workflows/mac-window.yml).
// Everywhere else the package's tests run as normal.
func TestMain(m *testing.M) {
	if os.Getenv("DSKY_MAC_WINDOW_TEST") != "1" {
		os.Exit(m.Run())
	}
	if err := nativeWindowCheck(); err != nil {
		fmt.Println("FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("PASS: native window")
	os.Exit(0)
}

// nativeWindowCheck opens the real window on a page that asks confirm(),
// prompt() and alert() as soon as it loads and reports the answers back.
// Getting the report proves the window loaded a loopback http page, ran its
// JavaScript, routed all three dialogs through the UI delegate, and delivered
// the answers — a boolean and a string — through WebKit's completion
// handlers. Then it asks the window to close from another goroutine, the way
// Quit does, and requires showWindow to return.
func nativeWindowCheck() error {
	results := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<!doctype html><title>DSKY window test</title>
<body style="background:#05050f;color:#00f5ff;font:20px monospace">window test
<script>
const c = confirm("confirm?");
const p = prompt("prompt?", "default");
alert("alert");
fetch("/result?c=" + c + "&p=" + encodeURIComponent(p));
document.body.textContent = "confirm=" + c + " prompt=" + p;
</script>`)
		case "/result":
			results <- "c=" + r.URL.Query().Get("c") + " p=" + r.URL.Query().Get("p")
		}
	}))
	defer srv.Close()

	var asked []string
	alertAnswer = func(message, field objc.ID) bool {
		asked = append(asked, objc.Send[string](message, sel("UTF8String")))
		if field != 0 {
			field.Send(sel("setStringValue:"), nsString("typed by the test"))
		}
		return true
	}

	var got string
	go func() {
		select {
		case got = <-results:
		case <-time.After(60 * time.Second):
			got = "timeout"
		}
		// Long enough for the page to paint its result for the screenshot.
		time.Sleep(2 * time.Second)
		if shot := os.Getenv("DSKY_SHOT"); shot != "" {
			_ = exec.Command("screencapture", "-x", shot).Run()
		}
		closeWindow()
	}()

	// A window that never closes hangs here; the workflow step's timeout is
	// what fails that case.
	if !showWindow(srv.URL+"/", "DSKY window test") {
		return fmt.Errorf("showWindow reported no window")
	}
	fmt.Printf("dialogs asked: %q\nreported: %s\n", asked, got)
	if got != "c=true p=typed by the test" {
		return fmt.Errorf("page reported %q, want confirm=true and the typed prompt", got)
	}
	if len(asked) != 3 {
		return fmt.Errorf("expected 3 dialogs, got %d", len(asked))
	}
	return nil
}
